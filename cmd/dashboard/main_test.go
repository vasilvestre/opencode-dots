package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// The transcript carries the agent messages the SSE stream omits.
func TestTranscriptFrame(t *testing.T) {
	line := `{"seq":6,"type":"message.assistant","payload":{"blocks":[{"type":"text","text":"I checked the\n  guard files first."}]}}`
	frame, ok := transcriptFrame(line)
	if !ok {
		t.Fatal("transcriptFrame refused an assistant message")
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(frame), &got); err != nil {
		t.Fatalf("frame is not JSON: %q", frame)
	}
	if got["kind"] != "message.assistant" || got["text"] != "I checked the guard files first." {
		t.Fatalf("unexpected frame %v", got)
	}
	if _, ok := transcriptFrame("not json"); ok {
		t.Fatal("transcriptFrame accepted a broken line")
	}
}

// Prices come from opencode's own database, read through the sqlite3 binary.
func TestQueryCost(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	dir := t.TempDir()
	db := filepath.Join(dir, "opencode.db")
	now := time.Now()
	sql := `create table session_v2 (id text, directory text, parent_id text, cost real,
		tokens_input integer, tokens_output integer, tokens_cache_read integer,
		time_created integer, time_updated integer);
		insert into session_v2 values
		('a','` + dir + `',null,0.5,100,10,1000,` + fmt.Sprint(now.UnixMilli()) + `,` + fmt.Sprint(now.UnixMilli()) + `),
		('b','` + dir + `/child',null,0.25,50,5,0,` + fmt.Sprint(now.UnixMilli()) + `,` + fmt.Sprint(now.UnixMilli()) + `),
		('c','/elsewhere',null,9,1,1,0,` + fmt.Sprint(now.UnixMilli()) + `,` + fmt.Sprint(now.UnixMilli()) + `);`
	if out, err := exec.Command("sqlite3", db, sql).CombinedOutput(); err != nil {
		t.Fatalf("creating the database: %v: %s", err, out)
	}

	d := &dash{state: dir}
	got, err := d.queryCost(db, now.Add(-time.Hour), now.Add(time.Hour))
	if err != nil {
		t.Fatalf("queryCost: %v", err)
	}
	if got.Cost != 0.75 || got.Sessions != 2 || got.Input != 150 {
		t.Fatalf("queryCost returned %+v, want the two sessions under the state directory", got)
	}
}

func TestReplayInputs(t *testing.T) {
	reply := `1 fetch step.started (running)
6 - message.user
   You answer a review comment on my pull request AlteredParadox/pmss-fleet-manager#235.
   Read /state/replies/pr235/pr.json`
	got := replayInputs(reply)
	if got["repo"] != "AlteredParadox/pmss-fleet-manager" || got["pr"] != "235" {
		t.Fatalf("reply-pr inputs = %v", got)
	}

	review := `6 - message.user
   You review pull request vasilvestre/opencode-dots#12.
   Read /state/reviews/pr12-9f8b7c6d5e4f3a2b1c0d9e8f7a6b5c4d3e2f1a0b/meta.json`
	got = replayInputs(review)
	if got["sha"] != "9f8b7c6d5e4f3a2b1c0d9e8f7a6b5c4d3e2f1a0b" {
		t.Fatalf("review-pr sha = %v", got)
	}

	issue := "You fix one reported defect in awf-project/cli, issue #42."
	got = replayInputs(issue)
	if got["repo"] != "awf-project/cli" || got["issue"] != "42" {
		t.Fatalf("fix-issue inputs = %v", got)
	}

	escaped := `"text":"You answer a review comment on my pull request\nawf-project/cli#447.\nRead"`
	got = replayInputs(escaped)
	if got["repo"] != "awf-project/cli" || got["pr"] != "447" {
		t.Fatalf("escaped prompt inputs = %v", got)
	}

	if n := len(replayInputs("nothing to see")); n != 0 {
		t.Fatalf("plain text produced %d inputs", n)
	}
}
