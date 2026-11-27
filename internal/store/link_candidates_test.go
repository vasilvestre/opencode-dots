package store

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

func TestLinkTokensReadStructuredFieldsOnly(t *testing.T) {
	cases := map[string]struct {
		payload string
		want    []string
	}{
		"advisory payload": {`{"id":"GHSA-cfgh-2345-wxvr","cves":["CVE-2026-1234"],"title":"see GHSA-rrvv-9999-qqwx in vendor/x",
			"packages":[{"name":"Symfony/Http-Kernel","affected":"<7"},{"name":"acme/lib"}]}`,
			[]string{"advisory:CVE-2026-1234", "advisory:GHSA-CFGH-2345-WXVR", "package:acme/lib", "package:symfony/http-kernel"}},
		"files": {`{"files":["./src/A.php",{"path":"src/B.php"},"../etc/passwd","/abs","a b"],"path":"src/A.php"}`,
			[]string{"file:src/A.php", "file:src/B.php"}},
		"free text is ignored":   {`{"title":"CVE-2026-9999 in acme/lib","body":"src/A.php","note":"GHSA-cfgh-2345-wxvr"}`, nil},
		"wrong shape is dropped": {`{"id":"not an id","packages":[{"name":"A B"}],"cves":[1,{"x":2}],"files":[["a"]]}`, nil},
		"not an object":          {`[1]`, nil},
		"not json":               {`{`, nil},
	}
	for name, c := range cases {
		if got := LinkTokens(json.RawMessage(c.payload)); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
	}
}

func TestLinkCandidates(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	a := actor(t, s)
	app, _, _ := s.UpsertProject(ctx, "default", "acme/app", 0, nil)
	app2, _, _ := s.UpsertProject(ctx, "default", "acme/app2", 1, nil)
	client, _, _ := s.UpsertProject(ctx, "default", "acme/client", 2, map[string]any{"trust": "client"})
	other, _, _ := s.UpsertProject(ctx, "elsewhere", "acme/app", 0, nil)
	mk := func(proj Project, title, ref, payload string) WorkItem {
		t.Helper()
		w, _, err := s.RecordSourced(ctx, Sourced{ProjectID: proj.ID, Ref: ref, Title: title, Payload: json.RawMessage(payload), Kind: "advisory", ActorID: a.ID})
		if err != nil {
			t.Fatal(err)
		}
		return w
	}
	pay := `{"id":"GHSA-cfgh-2345-wxvr","packages":[{"name":"acme/lib"}]}`
	w1 := mk(app, "one", "advisory:GHSA-cfgh-2345-wxvr@acme/app", pay)
	w2 := mk(app2, "two", "advisory:GHSA-cfgh-2345-wxvr@acme/app2", pay)
	w3 := mk(app, "three", "advisory:GHSA-mmpp-2222-ccgg@acme/app", `{"id":"GHSA-mmpp-2222-ccgg","packages":[{"name":"acme/lib"}]}`)
	mk(app, "unrelated", "advisory:GHSA-hhjj-7777-ffgg@acme/app", `{"id":"GHSA-hhjj-7777-ffgg","packages":[{"name":"other/pkg"}]}`)
	mk(client, "client", "advisory:GHSA-cfgh-2345-wxvr@acme/client", pay)
	mk(other, "elsewhere", "advisory:GHSA-cfgh-2345-wxvr@elsewhere/app", pay)
	mk(app, "text only", "jira:ABC-1", `{"title":"acme/lib GHSA-cfgh-2345-wxvr"}`)

	got, refused, err := s.LinkCandidates(ctx, "default", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || refused != 3 {
		t.Fatalf("%d candidates (want 3), %d refused (want 3): %+v", len(got), refused, got)
	}
	// Most shared tokens first: w1/w2 share the advisory and the package.
	if got[0].A.ID != w1.ID || got[0].B.ID != w2.ID || len(got[0].Shared) != 2 {
		t.Errorf("first = %+v", got[0])
	}
	for _, c := range got[1:] {
		if len(c.Shared) != 1 || c.Shared[0] != "package:acme/lib" {
			t.Errorf("pair = %+v", c)
		}
		if c.A.Trust != c.B.Trust || c.A.Trust != "open" {
			t.Errorf("a pair crosses a boundary: %+v", c)
		}
	}
	// The cap.
	if capped, _, _ := s.LinkCandidates(ctx, "default", 1); len(capped) != 1 {
		t.Errorf("cap: %d", len(capped))
	}

	// An open proposal, or any decided one, removes the pair.
	p, err := s.ProposeLink(ctx, LinkInput{A: w2.ID, B: w1.ID, Confidence: 0.8, ProposedBy: "bot"})
	if err != nil {
		t.Fatal(err)
	}
	if got, _, _ = s.LinkCandidates(ctx, "default", 10); len(got) != 2 {
		t.Errorf("after an open proposal: %d", len(got))
	}
	if _, err := s.DeclineLink(ctx, p.ID, "ada"); err != nil {
		t.Fatal(err)
	}
	if got, _, _ = s.LinkCandidates(ctx, "default", 10); len(got) != 2 {
		t.Errorf("after a decline: %d", len(got))
	}

	// A merged item is no longer a candidate.
	p2, _ := s.ProposeLink(ctx, LinkInput{A: w1.ID, B: w3.ID, Confidence: 0.8, ProposedBy: "bot"})
	if _, err := s.AcceptLink(ctx, p2.ID, "ada"); err != nil {
		t.Fatal(err)
	}
	if got, _, _ = s.LinkCandidates(ctx, "default", 10); len(got) != 0 {
		t.Errorf("after a merge: %+v", got)
	}
}
