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

// The history row names the target its log spells, and a log that names none
// settles once it stops changing instead of being read on every refresh.
func TestLogTargets(t *testing.T) {
	state := t.TempDir()
	os.MkdirAll(filepath.Join(state, "logs"), 0o755)
	id := "eeaaffff-1234-1234-1234-123456789012"
	log := filepath.Join(state, "logs", id+".jsonl")
	os.WriteFile(log, []byte(`data: {"text":"review awf-project/cli#447"}`+"\n"), 0o644)

	d := &dash{state: state, indexed: map[string]bool{}, targets: map[string]targetMemo{}}
	d.noteIndex(id, "review-pr", "2026-11-23T10:00:00Z")
	d.cacheTargets()

	rec := httptest.NewRecorder()
	d.logs(rec, httptest.NewRequest(http.MethodGet, "/api/logs", nil))
	var out []struct {
		Repo string `json:"repo"`
		PR   string `json:"pr"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || len(out) != 1 {
		t.Fatalf("api/logs returned %q (err %v), want 1 entry", rec.Body.String(), err)
	}
	if out[0].Repo != "awf-project/cli" || out[0].PR != "447" {
		t.Fatalf("api/logs lost the target: %+v", out[0])
	}

	// fix-issue can name the issue before the repository, so a guess without
	// the repository is read again once the log names it.
	part := "dddddddd-1234-1234-1234-123456789012"
	partLog := filepath.Join(state, "logs", part+".jsonl")
	os.WriteFile(partLog, []byte("data: {\"text\":\"issue #7\"}\n"), 0o644)
	d.noteIndex(part, "fix-issue", "2026-11-23T10:02:00Z")
	d.cacheTargets()
	if in := d.target(part); in["repo"] != "" {
		t.Fatalf("partial target = %v, want no repository yet", in)
	}
	os.WriteFile(partLog, []byte("data: {\"text\":\"in awf-project/cli, issue #7\"}\n"), 0o644)
	d.cacheTargets()
	if in := d.target(part); in["repo"] != "awf-project/cli" || in["issue"] != "7" {
		t.Fatalf("target after the log grew = %v, want awf-project/cli#7", in)
	}

	// A log that names no target is cached for the version read, so an
	// unchanged log is not parsed again.
	none := "bbbbcccc-1234-1234-1234-123456789012"
	noneLog := filepath.Join(state, "logs", none+".jsonl")
	os.WriteFile(noneLog, []byte("data: {\"kind\":\"step.started\"}\n"), 0o644)
	d.noteIndex(none, "tend-repo", "2026-11-23T10:05:00Z")
	d.cacheTargets()
	if in := d.target(none); len(in) != 0 {
		t.Fatalf("target = %v, want none", in)
	}
	st, err := os.Stat(noneLog)
	if err != nil {
		t.Fatal(err)
	}
	d.targetMu.Lock()
	memo := d.targets[none]
	d.targetMu.Unlock()
	if memo.size != st.Size() || !memo.mod.Equal(st.ModTime()) {
		t.Fatalf("cached log version = %d %v, want %d %v", memo.size, memo.mod, st.Size(), st.ModTime())
	}

	// Prune drops the cache with the index line it belongs to.
	os.Remove(log)
	os.Remove(noneLog)
	d.pruneLogs()
	d.targetMu.Lock()
	_, keptA := d.targets[id]
	_, keptB := d.targets[none]
	d.targetMu.Unlock()
	if keptA || keptB {
		t.Fatal("prune kept the target of a dropped log")
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

func TestRunCost(t *testing.T) {
	dir := t.TempDir()
	logs := filepath.Join(dir, "state", "logs")
	transcripts := filepath.Join(dir, "transcripts")
	for _, d := range []string{logs, transcripts} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	id := "abc12345-0000-0000-0000-000000000000"
	write := func(path, body string) {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	step := func(usd string) string {
		return `{"seq":4,"type":"step.completed","payload":{"name":"say","kind":"agent","cost_usd":` + usd + `}}` + "\n"
	}

	transcript := filepath.Join(transcripts, id+".jsonl")
	write(transcript, step("0.0025")+step("0.0010"))

	d := &dash{state: filepath.Join(dir, "state"), transcripts: transcripts, costs: map[string]costMemo{}}
	usd, steps := d.runCost(id)
	if steps != 2 || usd < 0.0034 || usd > 0.0036 {
		t.Fatalf("runCost = %v over %d steps", usd, steps)
	}

	write(filepath.Join(logs, id+".jsonl"), step("0.5"))
	if usd, steps = d.runCost(id); steps != 2 {
		t.Fatalf("transcript should win, got %d steps", steps)
	}

	if err := os.Remove(transcript); err != nil {
		t.Fatal(err)
	}
	if usd, steps = d.runCost(id); steps != 1 || usd != 0.5 {
		t.Fatalf("fallback to the recorder = %v over %d steps", usd, steps)
	}

	if usd, steps = d.runCost("ffffffff-0000-0000-0000-000000000000"); usd != 0 || steps != 0 {
		t.Fatalf("missing run = %v over %d steps", usd, steps)
	}
}

func TestParseSuggest(t *testing.T) {
	in := "Looks mergeable once the checks pass.\n\n" +
		"SUGGEST: review-pr repo=awf-project/cli pr=447\n" +
		"SUGGEST: reply-pr repo=\"awf-project/cli\" pr=447\n" +
		"SUGGEST: ../evil repo=x\n"
	text, got := parseSuggest(in)
	if strings.Contains(text, "SUGGEST") {
		t.Fatalf("suggestion lines left in the text: %q", text)
	}
	if !strings.Contains(text, "Looks mergeable") {
		t.Fatalf("text truncated: %q", text)
	}
	if len(got) != 2 {
		t.Fatalf("got %d suggestions, want 2 (the path-like name is refused): %+v", len(got), got)
	}
	if got[0].Workflow != "review-pr" || got[0].Inputs["pr"] != "447" || got[0].Inputs["repo"] != "awf-project/cli" {
		t.Fatalf("first suggestion = %+v", got[0])
	}
	if got[1].Inputs["repo"] != "awf-project/cli" {
		t.Fatalf("quoted value = %+v", got[1].Inputs)
	}
	// A plain answer yields no suggestion and keeps its text intact.
	plain, none := parseSuggest("No.\n")
	if plain != "No." || len(none) != 0 {
		t.Fatalf("plain answer = %q %+v", plain, none)
	}
}

// Tower indexes every non-hidden file without a .towerignore, so the Repos tab
// has to say when the file is missing or holds nothing.
func TestReposTowerWarning(t *testing.T) {
	dir := t.TempDir()
	checkout := filepath.Join(dir, "checkout")
	if err := os.MkdirAll(checkout, 0o755); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	awfgh := filepath.Join(root, "awf-gh.yaml")
	os.WriteFile(awfgh, []byte("repos:\n  - repo: o/watched\n    workflow: review-pr\n"), 0o644)
	os.WriteFile(filepath.Join(root, "repos.map"), []byte("o/watched="+checkout+"\n"), 0o644)
	opencode := filepath.Join(dir, "opencode.jsonc")
	os.WriteFile(opencode, []byte(`{"mcp":{"servers":{"tower":{"type":"local","disabled":false}}}}`), 0o644)

	d := &dash{state: dir, root: root, awfgh: awfgh, opencode: opencode}
	read := func() watchedRepo {
		rec := httptest.NewRecorder()
		d.repos(rec, httptest.NewRequest(http.MethodGet, "/api/repos", nil))
		var out []watchedRepo
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("api/repos returned %q (err %v)", rec.Body.String(), err)
		}
		if len(out) != 1 {
			t.Fatalf("api/repos returned %d repositories, want 1", len(out))
		}
		return out[0]
	}

	if got := read().TowerIgnore; got != "missing" {
		t.Fatalf("tower_ignore = %q with no file, want missing", got)
	}

	// A file with only a comment lists no pattern.
	os.WriteFile(filepath.Join(checkout, ".towerignore"), []byte("# nothing yet\n\n"), 0o644)
	if got := read().TowerIgnore; got != "empty" {
		t.Fatalf("tower_ignore = %q for a comment-only file, want empty", got)
	}

	os.WriteFile(filepath.Join(checkout, ".towerignore"), []byte("state/\nbin/\n"), 0o644)
	if got := read().TowerIgnore; got != "" {
		t.Fatalf("tower_ignore = %q for a filled file, want none", got)
	}

	// Tower off means no warning at all.
	os.WriteFile(opencode, []byte(`{"mcp":{"servers":{"tower":{"disabled":true}}}}`), 0o644)
	os.WriteFile(filepath.Join(checkout, ".towerignore"), nil, 0o644)
	if got := read().TowerIgnore; got != "" {
		t.Fatalf("tower_ignore = %q with tower disabled, want none", got)
	}
}
