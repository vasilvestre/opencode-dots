package main

import (
	"encoding/json"
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
