package main

import (
	"strings"
	"testing"
)

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
