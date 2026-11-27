package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"opencode-dots/internal/runrecorder"
)

// The page joins a history row to a log on workflow plus start second, so the
// index has to survive a restart and drop the lines of pruned logs.
func TestLogIndex(t *testing.T) {
	dir := t.TempDir()
	d := &dash{state: dir, rec: runrecorder.New(dir, "", nil)}

	os.MkdirAll(filepath.Join(dir, "logs"), 0o755)
	for _, id := range []string{"aaaabbbb-1234-1234-1234-123456789012", "ccccdddd-1234-1234-1234-123456789012"} {
		os.WriteFile(filepath.Join(dir, "logs", id+".jsonl"), []byte("data: {}\n"), 0o644)
	}
	noteRun(t, d, "aaaabbbb-1234-1234-1234-123456789012", "fix-issue", "2026-11-23T09:00:00Z")
	noteRun(t, d, "aaaabbbb-1234-1234-1234-123456789012", "fix-issue", "2026-11-23T09:00:00Z")
	noteRun(t, d, "ccccdddd-1234-1234-1234-123456789012", "reply-pr", "2026-11-23T09:05:00Z")

	if n := len(d.rec.Entries()); n != 2 {
		t.Fatalf("index holds %d ids after a duplicate write, want 2", n)
	}

	// A restart keeps the lines it can read back.
	if n := len(runrecorder.New(dir, "", nil).Entries()); n != 2 {
		t.Fatalf("index holds %d ids after reload, want 2", n)
	}

	rec := httptest.NewRecorder()
	d.logs(rec, httptest.NewRequest(http.MethodGet, "/api/logs", nil))
	var out []runrecorder.Entry
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || len(out) != 2 {
		t.Fatalf("api/logs returned %q (err %v), want 2 entries", rec.Body.String(), err)
	}
	if out[0].WorkflowName != "fix-issue" || out[0].StartedAt != "2026-11-23T09:00:00Z" {
		t.Fatalf("api/logs lost the workflow or start time: %+v", out[0])
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

	d := &dash{state: state, rec: runrecorder.New(state, "", nil)}
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

// The history row names the target its log spells, and a log that names none
// settles once it stops changing instead of being read on every refresh.
func TestLogTargets(t *testing.T) {
	state := t.TempDir()
	os.MkdirAll(filepath.Join(state, "logs"), 0o755)
	id := "eeaaffff-1234-1234-1234-123456789012"
	log := filepath.Join(state, "logs", id+".jsonl")
	os.WriteFile(log, []byte(`data: {"text":"review awf-project/cli#447"}`+"\n"), 0o644)

	d := &dash{state: state, rec: runrecorder.New(state, "", nil)}
	noteRun(t, d, id, "review-pr", "2026-11-23T10:00:00Z")
	d.cacheTargets(indexedIDs(d))

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
	noteRun(t, d, part, "fix-issue", "2026-11-23T10:02:00Z")
	d.cacheTargets(indexedIDs(d))
	if in := d.target(part); in["repo"] != "" {
		t.Fatalf("partial target = %v, want no repository yet", in)
	}
	os.WriteFile(partLog, []byte("data: {\"text\":\"in awf-project/cli, issue #7\"}\n"), 0o644)
	d.cacheTargets(indexedIDs(d))
	if in := d.target(part); in["repo"] != "awf-project/cli" || in["issue"] != "7" {
		t.Fatalf("target after the log grew = %v, want awf-project/cli#7", in)
	}

	// A log that names no target is cached for the version read, so an
	// unchanged log is not parsed again.
	none := "bbbbcccc-1234-1234-1234-123456789012"
	noneLog := filepath.Join(state, "logs", none+".jsonl")
	os.WriteFile(noneLog, []byte("data: {\"kind\":\"step.started\"}\n"), 0o644)
	noteRun(t, d, none, "tend-repo", "2026-11-23T10:05:00Z")
	d.cacheTargets(indexedIDs(d))
	if in := d.target(none); len(in) != 0 {
		t.Fatalf("target = %v, want none", in)
	}
	st, err := os.Stat(noneLog)
	if err != nil {
		t.Fatal(err)
	}
	if memo := d.targets.get(none); !memo.fresh(st) {
		t.Fatalf("cached log version = %d %v, want %d %v", memo.size, memo.mod, st.Size(), st.ModTime())
	}

	// Prune drops the cache with the index line it belongs to.
	os.Remove(log)
	os.Remove(noneLog)
	d.followRuns(nil)
	_, keptA := d.targets.m[id]
	_, keptB := d.targets.m[none]
	if keptA || keptB {
		t.Fatal("prune kept the target of a dropped log")
	}
}

// A finished review run carries its archived round diffs in the log index, but
// only when the log names a valid pull request and commit.
func TestLogRoundDiffs(t *testing.T) {
	state := t.TempDir()
	os.MkdirAll(filepath.Join(state, "logs"), 0o755)
	os.MkdirAll(filepath.Join(state, "reviews", "pr42-deadbeef"), 0o755)
	os.WriteFile(filepath.Join(state, "reviews", "pr42-deadbeef", "diff.round1.patch"), []byte("round 1\n"), 0o644)
	os.WriteFile(filepath.Join(state, "reviews", "pr42-deadbeef", "diff.round2.patch"), []byte("round 2\n"), 0o644)

	id := "abcd1234-1234-1234-1234-123456789012"
	log := filepath.Join(state, "logs", id+".jsonl")
	os.WriteFile(log, []byte(`data: {"text":"review awf-project/cli#42 pr42-deadbeef"}`+"\n"), 0o644)

	// A run whose log names no commit gets no rounds.
	plain := "efgh5678-1234-1234-1234-123456789012"
	plainLog := filepath.Join(state, "logs", plain+".jsonl")
	os.WriteFile(plainLog, []byte(`data: {"text":"review awf-project/cli#7"}`+"\n"), 0o644)

	d := &dash{state: state, rec: runrecorder.New(state, "", nil)}
	noteRun(t, d, id, "review-pr", "2026-11-25T10:00:00Z")
	noteRun(t, d, plain, "review-pr", "2026-11-25T10:05:00Z")
	d.cacheTargets(indexedIDs(d))

	rec := httptest.NewRecorder()
	d.logs(rec, httptest.NewRequest(http.MethodGet, "/api/logs", nil))
	var out []struct {
		Rounds []string `json:"rounds"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || len(out) != 2 {
		t.Fatalf("api/logs returned %q (err %v), want 2 entries", rec.Body.String(), err)
	}

	got := out[0].Rounds
	want := []string{"reviews/pr42-deadbeef/diff.round1.patch", "reviews/pr42-deadbeef/diff.round2.patch"}
	if len(got) != len(want) {
		t.Fatalf("rounds = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("rounds = %v, want %v", got, want)
		}
	}
	if n := len(out[1].Rounds); n != 0 {
		t.Fatalf("rounds for a run without a sha = %v, want none", out[1].Rounds)
	}
}

// noteRun adds a run to the index the way the recorder writes it.
func noteRun(t *testing.T, d *dash, id, workflow, started string) {
	t.Helper()
	line, _ := json.Marshal(runrecorder.Entry{ID: id, WorkflowName: workflow, StartedAt: started})
	f, err := os.OpenFile(filepath.Join(d.state, "logs", "index.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	f.Write(append(line, '\n'))
}

func indexedIDs(d *dash) []string {
	var ids []string
	for _, e := range d.rec.Entries() {
		ids = append(ids, e.ID)
	}
	return ids
}
