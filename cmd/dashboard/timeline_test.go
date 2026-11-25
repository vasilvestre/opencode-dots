package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

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

// Model inputs come from awf's state file, next to the transcript directory.
func TestRunModels(t *testing.T) {
	dir := t.TempDir()
	transcripts := filepath.Join(dir, "awf", "transcripts")
	states := filepath.Join(dir, "awf", "states")
	for _, d := range []string{transcripts, states} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	id := "abc12345-0000-0000-0000-000000000000"
	body := `{"inputs":{"review_model":"opencode/claude-sonnet-4-5","address_model":"opencode/gpt-5","pr":"447"}}`
	if err := os.WriteFile(filepath.Join(states, id+".json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	d := &dash{transcripts: transcripts}
	got := d.runModels(id)
	if len(got) != 2 || got["review_model"] != "opencode/claude-sonnet-4-5" || got["address_model"] != "opencode/gpt-5" {
		t.Fatalf("runModels = %v", got)
	}
	if _, ok := got["pr"]; ok {
		t.Fatalf("runModels kept a non-model input: %v", got)
	}

	if missing := d.runModels("ffffffff-0000-0000-0000-000000000000"); missing != nil {
		t.Fatalf("missing state = %v, want nil", missing)
	}
}

// A run shows its agent CLI. fix-issue also shows the route its router picked
// and the model of the child run, which awf keeps as a run of its own.
func TestRunInfo(t *testing.T) {
	dir := t.TempDir()
	transcripts := filepath.Join(dir, "awf", "transcripts")
	states := filepath.Join(dir, "awf", "states")
	for _, d := range []string{transcripts, states} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(id, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(states, id+".json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	claude := "aaaaaaaa-0000-0000-0000-000000000001"
	plain := "aaaaaaaa-0000-0000-0000-000000000002"
	parent := "aaaaaaaa-0000-0000-0000-000000000003"
	child := "aaaaaaaa-0000-0000-0000-000000000004"
	write(claude, `{"WorkflowName":"review-pr","Inputs":{"provider":"claude","review_model":"mistral/x","claude_review_model":"sonnet","pr":"4"}}`)
	write(plain, `{"WorkflowName":"ask-pr","Inputs":{"model":"mistral/x"}}`)
	write(parent, `{"WorkflowName":"fix-issue","Inputs":{"repo":"o/r","issue":"1"},"States":{"route":{"Output":"EXTERNAL"}}}`)
	write(child, `{"WorkflowName":"_fix-issue-external","ParentRunID":"`+parent+`","Inputs":{"model":"mistral/y","repo":"o/r","issue":"1"}}`)

	d := &dash{transcripts: transcripts}
	children := d.childStates()
	if got := children[parent]; got.WorkflowName != "_fix-issue-external" {
		t.Fatalf("child of the router = %+v", got)
	}

	if got := d.runInfo(claude, children); got.Provider != "claude" || got.Route != "" || got.Models["claude_review_model"] != "sonnet" {
		t.Fatalf("claude review = %+v", got)
	}
	if got := d.runInfo(plain, children); got.Provider != "opencode" || got.Route != "" {
		t.Fatalf("plain run = %+v", got)
	}
	got := d.runInfo(parent, children)
	if got.Provider != "opencode" || got.Route != "external" || got.Models["model"] != "mistral/y" {
		t.Fatalf("fix-issue router = %+v, want opencode, external and the child's model", got)
	}
	if got := d.runInfo("ffffffff-0000-0000-0000-000000000000", children); got.Provider != "" || got.Models != nil {
		t.Fatalf("missing state = %+v, want nothing", got)
	}
}
