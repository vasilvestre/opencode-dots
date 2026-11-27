package spend

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSumCost(t *testing.T) {
	usd, steps := SumCost("{\"cost_usd\":0.25}\n{\"x\":1}\n{\"cost_usd\": 1.5e-1, \"cost_usd\":0}\n")
	if usd != 0.4 || steps != 2 {
		t.Fatalf("usd=%v steps=%d, want 0.4 and 2", usd, steps)
	}
	if usd, steps := SumCost("no price here"); usd != 0 || steps != 0 {
		t.Fatalf("unpriced run: usd=%v steps=%d", usd, steps)
	}
}

func TestReplayInputs(t *testing.T) {
	in := ReplayInputs(`{"text":"Review acme/app#7 now\nthen pr7-abcdef1"}`)
	if in["repo"] != "acme/app" || in["pr"] != "7" || in["sha"] != "abcdef1" {
		t.Fatalf("inputs = %v", in)
	}
	in = ReplayInputs("work in acme/lib, issue #12 please")
	if in["repo"] != "acme/lib" || in["issue"] != "12" {
		t.Fatalf("issue inputs = %v", in)
	}
}

func writeRun(t *testing.T, state, id, started, prompt, cost string) string {
	t.Helper()
	logs := filepath.Join(state, "logs")
	if err := os.MkdirAll(logs, 0o755); err != nil {
		t.Fatal(err)
	}
	idx, err := os.OpenFile(filepath.Join(logs, "index.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()
	idx.WriteString(`{"execution_id":"` + id + `","workflow_name":"w","started_at":"` + started + `"}` + "\n")
	body := `{"text":"` + prompt + `"}` + "\n"
	if cost != "" {
		body += `{"cost_usd":` + cost + "}\n"
	}
	if err := os.WriteFile(filepath.Join(logs, id+".jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestDaySumsOneUTCDayPerRepository(t *testing.T) {
	state := t.TempDir()
	writeRun(t, state, "run00001", "2026-11-23T10:00:00Z", "Review acme/app#1", "0.50")
	writeRun(t, state, "run00002", "2026-11-23T23:30:00Z", "Review acme/app#2", "0.25")
	writeRun(t, state, "run00003", "2026-11-23T10:00:00Z", "Review acme/lib#3", "1")
	writeRun(t, state, "run00004", "2026-11-22T23:59:59Z", "Review acme/app#4", "9")
	writeRun(t, state, "run00005", "2026-11-24T00:00:00Z", "Review acme/app#5", "9")
	// 01:00 at +02:00 is 23:00 UTC on the 22nd: not the 23rd.
	writeRun(t, state, "run00006", "2026-11-23T01:00:00+02:00", "Review acme/app#6", "9")
	writeRun(t, state, "run00007", "2026-11-23T11:00:00Z", "Review acme/app#7", "")
	idx := filepath.Join(state, "logs", "index.jsonl")
	f, _ := os.OpenFile(idx, os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString("not json\n" + `{"execution_id":"run00008","started_at":"yesterday"}` + "\n" + `{"execution_id":"run00001","started_at":"2026-11-23T10:00:00Z"}` + "\n")
	f.Close()

	got, skipped, err := Day(state, nil, time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if got["acme/app"] != 0.75 || got["acme/lib"] != 1 || len(got) != 2 {
		t.Fatalf("per repository = %v", got)
	}
	if skipped != 1 {
		t.Fatalf("skipped = %d, want the one unreadable start time", skipped)
	}
}

func TestDayReadsTheTranscriptBeforeTheRecorderFile(t *testing.T) {
	state := t.TempDir()
	writeRun(t, state, "run00001", "2026-11-23T10:00:00Z", "Review acme/app#1", "0.10")
	tr := t.TempDir()
	if err := os.WriteFile(filepath.Join(tr, "run00001.jsonl"), []byte(`{"text":"Review acme/app#1"}`+"\n"+`{"cost_usd":2}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, _, err := Day(state, []string{tr}, time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC))
	if err != nil || got["acme/app"] != 2 {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestDayErrors(t *testing.T) {
	day := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
	if _, _, err := Day(filepath.Join(t.TempDir(), "nope"), nil, day); err == nil || !strings.Contains(err.Error(), "no run logs") {
		t.Fatalf("a missing logs directory must be an error, got %v", err)
	}
	state := t.TempDir()
	if err := os.MkdirAll(filepath.Join(state, "logs"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, _, err := Day(state, nil, day)
	if err != nil || len(got) != 0 {
		t.Fatalf("a logs directory with no index is no spend, got %v, %v", got, err)
	}
	if err := os.Mkdir(filepath.Join(state, "logs", "index.jsonl"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Day(state, nil, day); err == nil {
		t.Fatal("an unreadable index must be an error")
	}
}
