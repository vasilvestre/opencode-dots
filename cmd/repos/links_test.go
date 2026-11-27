package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"opencode-dots/internal/store"
)

func TestLinkCommands(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "dots.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	actor, _ := st.EnsureActor(ctx, store.Actor{Kind: "human", DisplayName: "Ada"})
	p, _, _ := st.UpsertProject(ctx, "default", "acme/app", 0, nil)
	w1, _ := st.CreateWorkItem(ctx, p.ID, "one", actor.ID, "github:acme/app#1")
	w2, _ := st.CreateWorkItem(ctx, p.ID, "two", actor.ID, "jira:ABC-1")
	w3, _ := st.CreateWorkItem(ctx, p.ID, "three", actor.ID, "sentry:s-1")
	run := func(action string, l dbLink) (string, error) {
		var out bytes.Buffer
		err := linkCommand(ctx, st, &out, action, l)
		return out.String(), err
	}

	if _, err := run("propose", dbLink{A: w1.ID, B: w2.ID}); err == nil {
		t.Error("propose without a confidence was accepted")
	}
	if _, err := run("propose", dbLink{A: w1.ID, B: w2.ID, Confidence: 2, ConfidenceSet: true}); err == nil {
		t.Error("propose with confidence 2 was accepted")
	}
	out, err := run("propose", dbLink{A: w1.ID, B: w2.ID, Confidence: 0.7, ConfidenceSet: true, Reason: "same\ncause\x07", Evidence: `{"k":1}`})
	if err != nil || !strings.HasPrefix(out, "link=lp_") || !strings.HasSuffix(out, "open\n") {
		t.Fatalf("propose = %q, %v", out, err)
	}
	id := strings.Fields(out)[0][len("link="):]
	if _, err := run("propose", dbLink{A: w2.ID, B: w1.ID, Confidence: 0.7, ConfidenceSet: true}); err == nil || !strings.Contains(err.Error(), "open link proposal") {
		t.Errorf("duplicate pair: %v", err)
	}

	file := filepath.Join(t.TempDir(), "p.json")
	os.WriteFile(file, []byte(`{"a":"`+w1.ID+`","b":"`+w3.ID+`","confidence":0.55,"reason":"from a file","proposed_by":"tend-repo"}`), 0o600)
	if out, err := run("propose", dbLink{File: file}); err != nil || !strings.Contains(out, "confidence=0.55") {
		t.Errorf("file propose = %q, %v", out, err)
	}
	os.WriteFile(file, []byte(`{"a":"x","b":"y","confidence":0.5,"surprise":1}`), 0o600)
	if _, err := run("propose", dbLink{File: file}); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Errorf("unknown key: %v", err)
	}
	os.WriteFile(file, []byte(`{"a":"x","b":"y"}`), 0o600)
	if _, err := run("propose", dbLink{File: file}); err == nil || !strings.Contains(err.Error(), "no confidence") {
		t.Errorf("no confidence: %v", err)
	}

	if out, _ := run("list", dbLink{}); strings.Count(out, "\n") != 2 || !strings.Contains(out, "same cause") || strings.Contains(out, "\x07") {
		t.Errorf("list = %q", out)
	}
	if _, err := run("accept", dbLink{}); err == nil {
		t.Error("accept without -id was accepted")
	}
	if out, err := run("accept", dbLink{ID: id, By: "ada"}); err != nil || out != "link="+id+" accepted\n" {
		t.Errorf("accept = %q, %v", out, err)
	}
	if _, err := run("accept", dbLink{ID: id}); err == nil {
		t.Error("a second accept was accepted")
	}
	if out, _ := run("list", dbLink{}); !strings.Contains(out, "accepted") || !strings.Contains(out, "open") {
		t.Errorf("list after = %q", out)
	}
	if _, err := run("split", dbLink{}); err == nil {
		t.Error("split without an id was accepted")
	}
	if _, err := run("split", dbLink{ID: "lp_nope"}); err == nil {
		t.Error("split of an unknown proposal was accepted")
	}
	if out, err := run("split", dbLink{ID: id, By: "grace"}); err != nil || out != "link="+id+" split\n" {
		t.Errorf("split = %q, %v", out, err)
	}
	if _, err := run("split", dbLink{ID: id}); err == nil {
		t.Error("a second split was accepted")
	}
	if out, _ := run("list", dbLink{}); !strings.Contains(out, "split") {
		t.Errorf("list after split = %q", out)
	}
	if _, err := run("bogus", dbLink{}); err == nil {
		t.Error("unknown action accepted")
	}
}

func TestLinkCandidatesWritesPairFiles(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "dots.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	actor, _ := st.EnsureActor(ctx, store.Actor{Kind: "bot", DisplayName: "adv"})
	p, _, _ := st.UpsertProject(ctx, "default", "acme/app", 0, nil)
	for i, ref := range []string{"advisory:GHSA-cfgh-2345-wxvr@acme/app", "advisory:GHSA-mmpp-2222-ccgg@acme/app"} {
		big := strings.Repeat("x", 5000)
		payload := `{"packages":[{"name":"acme/lib"}],"note":"` + big + `"}`
		if i == 0 {
			payload = `{"packages":[{"name":"acme/lib"}]}`
		}
		if _, _, err := st.RecordSourced(ctx, store.Sourced{ProjectID: p.ID, Ref: ref, Title: "t\nitle", Payload: []byte(payload), Kind: "advisory", ActorID: actor.ID}); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	if err := linkCommand(ctx, st, &out, "candidates", dbLink{Workspace: "default"}); err == nil {
		t.Error("candidates without -out was accepted")
	}
	dir := filepath.Join(t.TempDir(), "q")
	if err := linkCommand(ctx, st, &out, "candidates", dbLink{Workspace: "default", Out: dir, Max: 5}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "candidates=1 refused_boundary=0") || !strings.Contains(out.String(), "package:acme/lib") {
		t.Errorf("out = %q", out.String())
	}
	data, err := os.ReadFile(filepath.Join(dir, "pair-001.json"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.Contains(s, `"title":"t itle"`) || !strings.Contains(s, `"payload_truncated"`) || !strings.Contains(s, `"payload":{"packages"`) || len(s) > 6000 {
		t.Errorf("pair file = %s", s)
	}
}
