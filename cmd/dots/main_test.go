package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const failedID = "aaaaaaaa-1111-2222-3333-444444444444"
const okID = "bbbbbbbb-1111-2222-3333-444444444444"

func fixture(t *testing.T) string {
	t.Helper()
	state := t.TempDir()
	logs := filepath.Join(state, "logs")
	if err := os.MkdirAll(logs, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir()) // no transcripts from the real machine
	t.Setenv("XDG_DATA_HOME", "")
	index := `{"execution_id":"` + okID + `","workflow_name":"tend-repo","started_at":"2026-11-23T10:30:00Z"}
{"execution_id":"` + failedID + `","workflow_name":"review-pr","started_at":"2026-11-23T11:00:00+01:00"}
`
	failed := `data: {"kind":"step.started","seq":1,"status":"running","step":"fetch"}

data: {"kind":"step.completed","seq":2,"status":"completed","step":"fetch"}

data: {"kind":"step.started","seq":3,"status":"running","step":"review"}

data: {"kind":"step.failed","seq":4,"status":"failed","step":"review","error":"claude exit status 1"}

data: {"kind":"step.failed","seq":5,"status":"failed","step":"rework","error":"step review failed"}

data: {"kind":"workflow.failed","status":"failed","error":"terminal failure"}
`
	ok := `data: {"kind":"step.started","seq":1,"status":"running","step":"fetch"}
data: {"kind":"step.completed","seq":2,"status":"completed","step":"fetch"}
data: {"kind":"workflow.completed","status":"completed"}
`
	for name, body := range map[string]string{"index.jsonl": index, failedID + ".jsonl": failed, okID + ".jsonl": ok} {
		if err := os.WriteFile(filepath.Join(logs, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return state
}

func dots(t *testing.T, args ...string) (string, int) {
	t.Helper()
	var out, errw bytes.Buffer
	code := run(args, &out, &errw)
	return out.String() + errw.String(), code
}

func TestRunsFailedOnly(t *testing.T) {
	state := fixture(t)
	out, code := dots(t, "runs", "--state", state, "--failed", "--json")
	if code != 0 {
		t.Fatalf("code %d: %s", code, out)
	}
	var runs []struct{ ID, Status, Failed string }
	if err := json.Unmarshal([]byte(out), &runs); err != nil {
		t.Fatal(err, out)
	}
	if len(runs) != 1 || runs[0].ID != failedID {
		t.Fatalf("want only the failed run, got %+v", runs)
	}
}

func TestRunsNewestFirstOnTheInstant(t *testing.T) {
	// 11:00+01:00 is 10:00Z, earlier than 10:30Z although "11:00" sorts later
	// as text: the later instant must come first.
	state := fixture(t)
	out, _ := dots(t, "runs", "--state", state, "--json")
	if strings.Index(out, okID) > strings.Index(out, failedID) {
		t.Fatalf("newest instant must come first: %s", out)
	}
}

func TestShowNamesFirstFailureNotItsEcho(t *testing.T) {
	state := fixture(t)
	out, code := dots(t, "show", "aaaaaaaa", "--state", state)
	if code != 1 {
		t.Fatalf("a failed run exits 1, got %d", code)
	}
	if !strings.Contains(out, `first failure: step "review"`) || !strings.Contains(out, "claude exit status 1") {
		t.Fatalf("cause missing:\n%s", out)
	}
}

func TestFlagAfterReference(t *testing.T) {
	state := fixture(t)
	out, code := dots(t, "show", "last", "--state", state, "--json")
	if code != 0 || !strings.Contains(out, okID) {
		t.Fatalf("last is the newest run (%d): %s", code, out)
	}
}

func TestLogsFilter(t *testing.T) {
	state := fixture(t)
	out, code := dots(t, "logs", failedID, "--state", state, "--step", "review", "--kind", "step.failed")
	if code != 0 || strings.Count(strings.TrimSpace(out), "\n") != 0 || !strings.Contains(out, "claude exit status 1") {
		t.Fatalf("one failing event expected (%d):\n%s", code, out)
	}
}

func TestUnknownRef(t *testing.T) {
	state := fixture(t)
	if out, code := dots(t, "show", "zzzz", "--state", state); code != 2 {
		t.Fatalf("code %d: %s", code, out)
	}
}

func TestFirstFailureIsByEventOrderNotStartOrder(t *testing.T) {
	state := fixture(t)
	body := `data: {"kind":"step.started","seq":1,"step":"rework"}
data: {"kind":"step.started","seq":2,"step":"review"}
data: {"kind":"step.failed","seq":3,"status":"failed","step":"review","error":"boom"}
data: {"kind":"step.failed","seq":4,"status":"failed","step":"rework","error":"step review failed"}
data: {"kind":"workflow.failed","status":"failed"}
`
	if err := os.WriteFile(filepath.Join(state, "logs", failedID+".jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _ := dots(t, "show", failedID, "--state", state)
	if !strings.Contains(out, `first failure: step "review"`) {
		t.Fatalf("the child that failed first is the cause:\n%s", out)
	}
}
