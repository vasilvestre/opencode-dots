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
