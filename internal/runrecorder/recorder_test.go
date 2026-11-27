package runrecorder

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func write(t *testing.T, state, id, body string) {
	t.Helper()
	dir := filepath.Join(state, "logs")
	os.MkdirAll(dir, 0o755)
	if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

const (
	idA = "aaaabbbb-1234-1234-1234-123456789012"
	idB = "ccccdddd-1234-1234-1234-123456789012"
)

// The page joins a history row to a log on workflow plus start second, so the index has to
// survive a restart and drop the lines of pruned logs.
func TestIndexSurvivesARestartAndPruneDropsGoneLogs(t *testing.T) {
	state := t.TempDir()
	write(t, state, idA, "data: {}\n")
	write(t, state, idB, "data: {}\n")
	r := New(state, "", nil)
	r.Note(idA, "fix-issue", "2026-11-23T09:00:00Z")
	r.Note(idA, "fix-issue", "2026-11-23T09:00:00Z")
	r.Note(idB, "reply-pr", "2026-11-23T09:05:00Z")
	if got := r.Entries(); len(got) != 2 || got[0].WorkflowName != "fix-issue" || got[0].StartedAt != "2026-11-23T09:00:00Z" {
		t.Fatalf("entries after a duplicate write = %+v", got)
	}
	if got := New(state, "", nil).Entries(); len(got) != 2 {
		t.Fatalf("entries after reload = %+v", got)
	}
	if e, ok := r.Entry(idB); !ok || e.WorkflowName != "reply-pr" {
		t.Fatalf("entry = %+v %v", e, ok)
	}

	os.Remove(r.Path(idB))
	r.Prune()
	if ids := r.IDs(); !slices.Equal(ids, []string{idA}) || r.Has(idB) {
		t.Fatalf("ids after prune = %v", ids)
	}
	if got := r.Entries(); len(got) != 1 || got[0].ID != idA {
		t.Fatalf("index after prune = %+v", got)
	}
}

func TestAnnounceOncePerRunAndOnlyOnCompletedOrFailed(t *testing.T) {
	state := t.TempDir()
	var sent []string
	r := New(state, "", func(id, workflow, status string) { sent = append(sent, id+" "+workflow+" "+status) })
	write(t, state, "done-run-1", "data: {\"kind\":\"step.started\"}\n\ndata: {\"kind\":\"workflow.completed\"}\n")
	write(t, state, "bad-run-01", `data: {"kind":"workflow.failed"}`+"\n")
	write(t, state, "cancelled-1", `data: {"kind":"workflow.cancelled"}`+"\n")
	write(t, state, "running-01", `data: {"kind":"step.started"}`+"\n")
	for range 2 {
		r.announce("done-run-1", "review-pr")
		r.announce("bad-run-01", "fix-issue")
		r.announce("cancelled-1", "fix-issue")
		r.announce("running-01", "fix-issue")
	}
	if want := []string{"done-run-1 review-pr completed", "bad-run-01 fix-issue failed"}; !slices.Equal(sent, want) {
		t.Fatalf("sent %v, want %v", sent, want)
	}
	if !r.Ended("cancelled-1") || r.Ended("running-01") {
		t.Fatal("Ended must see any terminal event and only that")
	}
}

func TestLogsAlreadyOnDiskCountAsAnnounced(t *testing.T) {
	state := t.TempDir()
	write(t, state, "seeded-run-1", `data: {"kind":"workflow.completed"}`+"\n")
	var sent []string
	r := New(state, "", func(id, _, _ string) { sent = append(sent, id) })
	r.announce("seeded-run-1", "review-pr")
	if len(sent) != 0 {
		t.Fatalf("a restart announced %v", sent)
	}
}

func TestValidIDKeepsAPathOutOfTheFilesystem(t *testing.T) {
	for id, want := range map[string]bool{idA: true, "short": false, "../../etc/passwd": false, strings.Repeat("a", 65): false, "has space 1": false} {
		if ValidID(id) != want {
			t.Errorf("ValidID(%q) = %v", id, !want)
		}
	}
}
