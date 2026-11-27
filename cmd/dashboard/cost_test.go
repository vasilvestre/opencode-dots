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
	if _, steps = d.runCost(id); steps != 2 {
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

func TestUsageGroupsByPullRequest(t *testing.T) {
	dir := t.TempDir()
	d := &dash{state: dir, awf: "http://127.0.0.1:1", costs: map[string]costMemo{}}
	if err := os.MkdirAll(filepath.Join(dir, "logs"), 0o755); err != nil {
		t.Fatal(err)
	}
	runs := []struct{ id, workflow, prompt, cost string }{
		{"aaaaaaa1", "review-pr", "Review awf-project/cli#447 now", "0.01"},
		{"aaaaaaa2", "reply-pr", "Answer awf-project/cli#447 now", ""},
		{"aaaaaaa3", "review-pr", "Review other/repo#7 now", "0.02"},
	}
	var index strings.Builder
	for _, r := range runs {
		prompt := `{"kind":"message.user","text":"` + r.prompt + `"}`
		cost := ""
		if r.cost != "" {
			cost = `
{"kind":"agent","cost_usd":` + r.cost + `}`
		}
		body := "{\"seq\":1}\n" + prompt + cost + "\n"
		if err := os.WriteFile(filepath.Join(dir, "logs", r.id+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		index.WriteString(`{"execution_id":"` + r.id + `","workflow_name":"` + r.workflow + `","started_at":"2026-11-23T10:00:00Z"}` + "\n")
	}
	if err := os.WriteFile(filepath.Join(dir, "logs", "index.jsonl"), []byte(index.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	d.usage(rec, httptest.NewRequest(http.MethodGet, "/api/usage", nil))

	var got struct {
		Total    float64    `json:"total"`
		Runs     int        `json:"runs"`
		Unpriced int        `json:"unpriced"`
		Groups   []usageOne `json:"groups"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	if got.Runs != 3 || got.Unpriced != 1 || got.Total != 0.03 {
		t.Fatalf("runs=%d unpriced=%d total=%v", got.Runs, got.Unpriced, got.Total)
	}
	if len(got.Groups) != 2 {
		t.Fatalf("groups=%d, want 2: %s", len(got.Groups), rec.Body.String())
	}
	top := got.Groups[0]
	if top.Key != "other/repo#7" || top.Cost != 0.02 {
		t.Fatalf("top group = %+v", top)
	}
	cli := got.Groups[1]
	if cli.Key != "awf-project/cli#447" || cli.Runs != 2 || len(cli.Workflows) != 2 {
		t.Fatalf("cli group = %+v", cli)
	}
}
