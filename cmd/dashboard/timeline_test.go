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
