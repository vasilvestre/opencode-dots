package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

type linkFix struct {
	s      *Store
	a      Actor
	proj   Project
	a1, a2 WorkItem // a1 holds github:acme/app#1, a2 holds jira:ABC-2 and jira:ABC-3
}

func newLinkFix(t *testing.T) linkFix {
	t.Helper()
	ctx := context.Background()
	s := open(t)
	a := actor(t, s)
	p, _, err := s.UpsertProject(ctx, "default", "acme/app", 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	w1, err := s.CreateWorkItem(ctx, p.ID, "Crash on login", a.ID, "github:acme/app#1")
	if err != nil {
		t.Fatal(err)
	}
	w2, err := s.CreateWorkItem(ctx, p.ID, "Login fails", a.ID, "jira:ABC-2", "jira:ABC-3")
	if err != nil {
		t.Fatal(err)
	}
	return linkFix{s: s, a: a, proj: p, a1: w1, a2: w2}
}

func (f linkFix) propose(t *testing.T, a, b string) LinkProposal {
	t.Helper()
	p, err := f.s.ProposeLink(context.Background(), LinkInput{A: a, B: b, Confidence: 0.8, Reason: "same stack\ntrace", ProposedBy: "tend-repo"})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestProposeLinkRecordsDataAndChangesNothing(t *testing.T) {
	ctx := context.Background()
	f := newLinkFix(t)
	p, err := f.s.ProposeLink(ctx, LinkInput{A: f.a1.ID, B: f.a2.ID, Confidence: 0.9,
		Reason: "  same\x00 stack\n\ntrace\t" + strings.Repeat("x", 900), Evidence: json.RawMessage(`{ "frames": ["a"] }`), ProposedBy: "tend\nrepo"})
	if err != nil {
		t.Fatal(err)
	}
	if p.State != "open" || p.ProposedBy != "tend repo" || strings.ContainsAny(p.Reason, "\n\t\x00") || len([]rune(p.Reason)) > MaxLinkReason {
		t.Errorf("proposal = %+v", p)
	}
	if !strings.HasPrefix(p.Reason, "same stack trace x") || string(p.Evidence) != `{"frames":["a"]}` {
		t.Errorf("reason %q evidence %s", p.Reason, p.Evidence)
	}
	if p.ItemA.Repo != "acme/app" || len(p.ItemB.Refs) != 2 || p.ItemA.Trust != "open" {
		t.Errorf("items = %+v %+v", p.ItemA, p.ItemB)
	}
	// Nothing moved.
	w2, _ := f.s.workItem(ctx, f.a2.ID)
	if w2.State != "open" || len(w2.Refs) != 2 {
		t.Errorf("a proposal changed an item: %+v", w2)
	}
	for _, id := range []string{f.a1.ID, f.a2.ID} {
		ev, _ := f.s.WorkItemEvents(ctx, id)
		if len(ev) != 1 || ev[0].Kind != "link.proposed" {
			t.Errorf("events of %s = %+v", id, ev)
		}
	}
}

func TestProposeLinkValidates(t *testing.T) {
	ctx := context.Background()
	f := newLinkFix(t)
	base := LinkInput{A: f.a1.ID, B: f.a2.ID, Confidence: 0.5, ProposedBy: "bot"}
	bad := map[string]func(*LinkInput){
		"same item":      func(i *LinkInput) { i.B = i.A },
		"confidence > 1": func(i *LinkInput) { i.Confidence = 1.5 },
		"confidence < 0": func(i *LinkInput) { i.Confidence = -0.1 },
		"no proposer":    func(i *LinkInput) { i.ProposedBy = " \n" },
		"evidence array": func(i *LinkInput) { i.Evidence = json.RawMessage(`[1]`) },
		"evidence big": func(i *LinkInput) {
			i.Evidence = json.RawMessage(`{"a":"` + strings.Repeat("x", MaxLinkEvidence) + `"}`)
		},
	}
	for name, mod := range bad {
		in := base
		mod(&in)
		if _, err := f.s.ProposeLink(ctx, in); !errors.Is(err, ErrLinkInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	in := base
	in.B = "wi_missing"
	if _, err := f.s.ProposeLink(ctx, in); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown item: %v", err)
	}
}

func TestAPairIsUniqueInEitherOrderWhileOpen(t *testing.T) {
	ctx := context.Background()
	f := newLinkFix(t)
	p := f.propose(t, f.a1.ID, f.a2.ID)
	for _, in := range []LinkInput{{A: f.a1.ID, B: f.a2.ID}, {A: f.a2.ID, B: f.a1.ID}} {
		in.Confidence, in.ProposedBy = 0.5, "bot"
		if _, err := f.s.ProposeLink(ctx, in); !errors.Is(err, ErrLinkExists) {
			t.Errorf("duplicate %v: err = %v", in.A, err)
		}
	}
	if _, err := f.s.DeclineLink(ctx, p.ID, "ada"); err != nil {
		t.Fatal(err)
	}
	// Once decided, the pair may be proposed again.
	f.propose(t, f.a2.ID, f.a1.ID)
	list, _ := f.s.ListLinkProposals(ctx, 0)
	if len(list) != 2 {
		t.Errorf("list = %d", len(list))
	}
}

func TestAcceptLinkMovesReferencesAndKeepsTheMergedItem(t *testing.T) {
	ctx := context.Background()
	f := newLinkFix(t)
	p := f.propose(t, f.a1.ID, f.a2.ID)
	got, err := f.s.AcceptLink(ctx, p.ID, "ada")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "accepted" || got.DecidedBy != "ada" || got.DecidedAt.IsZero() {
		t.Errorf("proposal = %+v", got)
	}
	first, _ := f.s.workItem(ctx, f.a1.ID)
	if len(first.Refs) != 3 {
		t.Errorf("the first item holds %d refs, want 3", len(first.Refs))
	}
	byRef, err := f.s.WorkItemByRef(ctx, "jira:ABC-3")
	if err != nil || byRef.ID != f.a1.ID {
		t.Errorf("jira:ABC-3 is on %v (%v)", byRef.ID, err)
	}
	second, _ := f.s.workItem(ctx, f.a2.ID)
	if second.State != "merged" || len(second.Refs) != 0 {
		t.Errorf("second = %+v", second)
	}
	it, _ := f.s.linkItem(ctx, f.a2.ID)
	if it.MergedInto != f.a1.ID {
		t.Errorf("merged_into = %q", it.MergedInto)
	}
	for id, kind := range map[string]string{f.a1.ID: "link.accepted", f.a2.ID: "link.merged"} {
		ev, _ := f.s.WorkItemEvents(ctx, id)
		if len(ev) != 2 || ev[1].Kind != kind || !strings.Contains(string(ev[1].Data), "jira:ABC-2") || !strings.Contains(string(ev[1].Data), p.ID) {
			t.Errorf("events of %s = %+v", id, ev)
		}
	}
	// Decided: neither again nor the other way, and the merged item takes no new proposal.
	if _, err := f.s.AcceptLink(ctx, p.ID, "ada"); !errors.Is(err, ErrLinkClosed) {
		t.Errorf("second accept: %v", err)
	}
	if _, err := f.s.DeclineLink(ctx, p.ID, "ada"); !errors.Is(err, ErrLinkClosed) {
		t.Errorf("decline after accept: %v", err)
	}
	if _, err := f.s.ProposeLink(ctx, LinkInput{A: f.a1.ID, B: f.a2.ID, Confidence: 0.5, ProposedBy: "bot"}); !errors.Is(err, ErrLinkClosed) {
		t.Errorf("propose on a merged item: %v", err)
	}
}

func TestAcceptLinkRefusesAcrossATrustBoundaryOrWorkspace(t *testing.T) {
	ctx := context.Background()
	f := newLinkFix(t)
	client, _, _ := f.s.UpsertProject(ctx, "default", "acme/client", 1, map[string]any{"trust": "client"})
	other, _, _ := f.s.UpsertProject(ctx, "elsewhere", "acme/app", 0, nil)
	for name, pid := range map[string]string{"trust": client.ID, "workspace": other.ID} {
		w, err := f.s.CreateWorkItem(ctx, pid, "same defect", f.a.ID, "sentry:"+name+"-1")
		if err != nil {
			t.Fatal(err)
		}
		p := f.propose(t, f.a1.ID, w.ID)
		if _, err := f.s.AcceptLink(ctx, p.ID, "ada"); !errors.Is(err, ErrLinkBoundary) {
			t.Fatalf("%s: err = %v", name, err)
		}
		again, _ := f.s.LinkProposalByID(ctx, p.ID)
		moved, _ := f.s.workItem(ctx, w.ID)
		if again.State != "open" || moved.State != "open" || len(moved.Refs) != 1 {
			t.Errorf("%s: the refusal changed something: %+v %+v", name, again, moved)
		}
		ev, _ := f.s.WorkItemEvents(ctx, w.ID)
		if len(ev) != 1 {
			t.Errorf("%s: events = %+v", name, ev)
		}
		// A person may still decline it.
		if _, err := f.s.DeclineLink(ctx, p.ID, "ada"); err != nil {
			t.Errorf("%s: decline: %v", name, err)
		}
	}
	// The same level on two projects is fine.
	c2, _, _ := f.s.UpsertProject(ctx, "default", "acme/client2", 2, map[string]any{"trust": "client"})
	w3, _ := f.s.CreateWorkItem(ctx, client.ID, "a", f.a.ID, "sentry:c-1")
	w4, _ := f.s.CreateWorkItem(ctx, c2.ID, "b", f.a.ID, "sentry:c-2")
	if _, err := f.s.AcceptLink(ctx, f.propose(t, w3.ID, w4.ID).ID, "ada"); err != nil {
		t.Errorf("same trust, two projects: %v", err)
	}
}

func TestAcceptLinkRefusesWhileTheSecondItemIsClaimed(t *testing.T) {
	ctx := context.Background()
	f := newLinkFix(t)
	if _, err := f.s.TakeClaim(ctx, f.a2.ID, f.a.ID, "laptop", "fix", time.Hour); err != nil {
		t.Fatal(err)
	}
	p := f.propose(t, f.a1.ID, f.a2.ID)
	if _, err := f.s.AcceptLink(ctx, p.ID, "ada"); !errors.Is(err, ErrLinkClosed) {
		t.Errorf("err = %v", err)
	}
}

func TestDeclineLinkRecordsEventsAndTouchesNothingElse(t *testing.T) {
	ctx := context.Background()
	f := newLinkFix(t)
	p := f.propose(t, f.a1.ID, f.a2.ID)
	got, err := f.s.DeclineLink(ctx, p.ID, "ada")
	if err != nil || got.State != "declined" || got.DecidedBy != "ada" {
		t.Fatalf("decline = %+v, %v", got, err)
	}
	w2, _ := f.s.workItem(ctx, f.a2.ID)
	if w2.State != "open" || len(w2.Refs) != 2 {
		t.Errorf("w2 = %+v", w2)
	}
	for _, id := range []string{f.a1.ID, f.a2.ID} {
		ev, _ := f.s.WorkItemEvents(ctx, id)
		if len(ev) != 2 || ev[1].Kind != "link.declined" {
			t.Errorf("events of %s = %+v", id, ev)
		}
	}
	if _, err := f.s.DeclineLink(ctx, "lp_missing", "ada"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown: %v", err)
	}
	if _, err := f.s.DeclineLink(ctx, p.ID, ""); !errors.Is(err, ErrLinkInvalid) {
		t.Errorf("no decider: %v", err)
	}
}

func TestListLinkProposalsIsNewestFirstAndBounded(t *testing.T) {
	ctx := context.Background()
	f := newLinkFix(t)
	w3, _ := f.s.CreateWorkItem(ctx, f.proj.ID, "third", f.a.ID, "sentry:3")
	first := f.propose(t, f.a1.ID, f.a2.ID)
	second := f.propose(t, f.a1.ID, w3.ID)
	list, err := f.s.ListLinkProposals(ctx, 0)
	if err != nil || len(list) != 2 || list[0].ID != second.ID || list[1].ID != first.ID {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if one, _ := f.s.ListLinkProposals(ctx, 1); len(one) != 1 {
		t.Errorf("limit 1 gave %d", len(one))
	}
}

func TestMigration6IsAdditive(t *testing.T) {
	f := newLinkFix(t)
	if v, _ := f.s.schemaVersion(context.Background()); v < 6 {
		t.Errorf("schema version = %d", v)
	}
	w, _ := f.s.workItem(context.Background(), f.a1.ID)
	if w.State != "open" {
		t.Errorf("state = %q", w.State)
	}
}
