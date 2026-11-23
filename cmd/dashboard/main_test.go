package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The page joins a history row to a log on workflow plus start second, so the
// index has to survive a restart and drop the lines of pruned logs.
func TestLogIndex(t *testing.T) {
	dir := t.TempDir()
	d := &dash{state: dir, indexed: map[string]bool{}}

	os.MkdirAll(filepath.Join(dir, "logs"), 0o755)
	for _, id := range []string{"aaaabbbb-1234-1234-1234-123456789012", "ccccdddd-1234-1234-1234-123456789012"} {
		os.WriteFile(filepath.Join(dir, "logs", id+".jsonl"), []byte("data: {}\n"), 0o644)
	}
	d.noteIndex("aaaabbbb-1234-1234-1234-123456789012", "fix-issue", "2026-11-23T09:00:00Z")
	d.noteIndex("aaaabbbb-1234-1234-1234-123456789012", "fix-issue", "2026-11-23T09:00:00Z")
	d.noteIndex("ccccdddd-1234-1234-1234-123456789012", "reply-pr", "2026-11-23T09:05:00Z")

	if n := len(d.loadIndexed()); n != 2 {
		t.Fatalf("index holds %d ids after a duplicate write, want 2", n)
	}

	// A restart keeps the lines it can read back.
	if n := len((&dash{state: dir}).loadIndexed()); n != 2 {
		t.Fatalf("index holds %d ids after reload, want 2", n)
	}

	rec := httptest.NewRecorder()
	d.logs(rec, httptest.NewRequest(http.MethodGet, "/api/logs", nil))
	var out []logIndex
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || len(out) != 2 {
		t.Fatalf("api/logs returned %q (err %v), want 2 entries", rec.Body.String(), err)
	}
	if out[0].WorkflowName != "fix-issue" || out[0].StartedAt != "2026-11-23T09:00:00Z" {
		t.Fatalf("api/logs lost the workflow or start time: %+v", out[0])
	}

	// Prune drops the line whose log is gone and keeps the other.
	os.Remove(filepath.Join(dir, "logs", "ccccdddd-1234-1234-1234-123456789012.jsonl"))
	d.indexed = d.loadIndexed() // prune copies its survivors back into the map
	d.pruneLogs()
	if n := len(d.indexed); n != 1 {
		t.Fatalf("index holds %d ids after prune, want 1", n)
	}
	if !d.indexed["aaaabbbb-1234-1234-1234-123456789012"] {
		t.Fatal("prune dropped the log that still exists")
	}
}

func TestResolveEditable(t *testing.T) {
	d := &dash{root: "/repo"}
	ok := []string{
		"workflows/review-pr.yaml",
		"workflows/a_b-9.yaml",
		"./workflows/reply-pr.yaml",
		"awf-gh.yaml",
		"tend.list",
		"repos.map",
	}
	for _, rel := range ok {
		if _, got := d.resolveEditable(rel); !got {
			t.Errorf("refused %q, want accepted", rel)
		}
	}
	bad := []string{
		"",
		"workflows/../secrets.yaml",
		"../awf-gh.yaml",
		"workflows/nested/x.yaml",
		"workflows/x.yml",
		"workflows/.hidden.yaml",
		"state/logs/x.jsonl",
		"/etc/passwd",
		"awf-gh.yaml.bak",
	}
	for _, rel := range bad {
		if abs, got := d.resolveEditable(rel); got {
			t.Errorf("accepted %q as %q, want refused", rel, abs)
		}
	}
}

func TestSaveFile(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "workflows"), 0o755)
	target := filepath.Join(root, "workflows", "review-pr.yaml")
	os.WriteFile(target, []byte("name: old\n"), 0o644)
	d := &dash{root: root}

	save := func(body, origin string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPut, "/api/save", strings.NewReader(body))
		req.Host = "127.0.0.1:8099"
		req.Header.Set("Content-Type", "application/json")
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		rec := httptest.NewRecorder()
		d.saveFile(rec, req)
		return rec
	}

	if rec := save(`{"path":"workflows/review-pr.yaml","content":"name: new\nversion: 2\n"}`, "http://127.0.0.1:8099"); rec.Code != 200 {
		t.Fatalf("save returned %d: %s", rec.Code, rec.Body)
	}
	if got, _ := os.ReadFile(target); string(got) != "name: new\nversion: 2\n" {
		t.Fatalf("file holds %q", got)
	}
	backups, _ := filepath.Glob(target + ".*.bak")
	if len(backups) != 1 {
		t.Fatalf("want one backup, got %v", backups)
	}
	if old, _ := os.ReadFile(backups[0]); string(old) != "name: old\n" {
		t.Fatalf("backup holds %q", old)
	}

	// Invalid YAML is refused and the disk keeps the last good content.
	if rec := save(`{"path":"workflows/review-pr.yaml","content":"name: [unclosed\n"}`, "http://127.0.0.1:8099"); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid YAML returned %d, want 422", rec.Code)
	}
	if got, _ := os.ReadFile(target); string(got) != "name: new\nversion: 2\n" {
		t.Fatalf("invalid YAML reached the disk: %q", got)
	}

	if rec := save(`{"path":"workflows/review-pr.yaml","content":"name: x\n"}`, "http://evil.example"); rec.Code != http.StatusForbidden {
		t.Fatalf("foreign origin returned %d, want 403", rec.Code)
	}
	if rec := save(`{"path":"/etc/passwd","content":"x"}`, "http://127.0.0.1:8099"); rec.Code != http.StatusForbidden {
		t.Fatalf("foreign path returned %d, want 403", rec.Code)
	}
	if rec := save(`{"path":"tend.list","content":"x"}`, "http://127.0.0.1:8099"); rec.Code != 200 {
		t.Fatalf("plain list save returned %d", rec.Code)
	}
}

func TestLoopbackRequest(t *testing.T) {
	cases := []struct {
		host, origin string
		want         bool
	}{
		{"127.0.0.1:8099", "", true},
		{"localhost:8099", "http://localhost:8099", true},
		{"[::1]:8099", "http://[::1]:8099", true},
		{"evil.example", "", false},
		{"127.0.0.1:8099", "http://evil.example", false},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodPut, "/api/save", nil)
		req.Host = c.host
		if c.origin != "" {
			req.Header.Set("Origin", c.origin)
		}
		if got := loopbackRequest(req); got != c.want {
			t.Errorf("loopbackRequest(host %q, origin %q) = %v, want %v", c.host, c.origin, got, c.want)
		}
	}
}

// The run dialog offers every repository the machine already knows, from the
// watcher config, the daily list, the path map and the state directories.
func TestRepoNames(t *testing.T) {
	root, state := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(root, "awf-gh.yaml"), []byte("repos:\n  - repo: owner/watched\n    workflow: review-pr\n"), 0o644)
	os.WriteFile(filepath.Join(root, "tend.list"), []byte("# comment\nowner/tended true\nnot a repo\n"), 0o644)
	os.WriteFile(filepath.Join(root, "repos.map"), []byte("owner/mapped=/tmp/x\n"), 0o644)
	os.MkdirAll(filepath.Join(state, "tend", "owner", "learned"), 0o755)

	d := &dash{root: root, state: state, awfgh: filepath.Join(root, "awf-gh.yaml")}
	rec := httptest.NewRecorder()
	d.repoNames(rec, httptest.NewRequest(http.MethodGet, "/api/repos/names", nil))

	var got []string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("api/repos/names returned %q: %v", rec.Body.String(), err)
	}
	want := []string{"owner/learned", "owner/mapped", "owner/tended", "owner/watched"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("api/repos/names returned %v, want %v", got, want)
	}
}

// The page tails the recorder's file, so the stream must replay what happened
// before the page opened and stop at the terminal event.
func TestStreamReplaysAndStops(t *testing.T) {
	state := t.TempDir()
	os.MkdirAll(filepath.Join(state, "logs"), 0o755)
	id := "stream-probe-1"
	os.WriteFile(filepath.Join(state, "logs", id+".jsonl"), []byte(
		"data: {\"kind\":\"step.started\",\"seq\":1,\"step\":\"one\"}\n\n"+
			"data: {\"kind\":\"workflow.completed\",\"status\":\"completed\"}\n\n"+
			"data: {\"kind\":\"step.started\",\"seq\":9,\"step\":\"late\"}\n\n"), 0o644)

	d := &dash{state: state}
	rec := httptest.NewRecorder()
	d.stream(rec, httptest.NewRequest(http.MethodGet, "/api/stream?id="+id, nil))

	body := rec.Body.String()
	if !strings.Contains(body, `"step":"one"`) || !strings.Contains(body, `"workflow.completed"`) {
		t.Fatalf("stream did not replay the frames: %q", body)
	}
	if strings.Contains(body, `"step":"late"`) {
		t.Fatalf("stream kept reading after the terminal frame: %q", body)
	}
}
