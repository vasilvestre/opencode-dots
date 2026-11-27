package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func sourcedFixture(t *testing.T) (*Store, Project, Actor) {
	t.Helper()
	s := open(t)
	p, _, err := s.UpsertProject(context.Background(), "default", "acme/app", 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	return s, p, actor(t, s)
}

func TestRecordSourcedIsIdempotentAndRecordsChanges(t *testing.T) {
	ctx := context.Background()
	s, p, a := sourcedFixture(t)
	in := Sourced{ProjectID: p.ID, Ref: "advisory:GHSA-aaaa-bbbb-cccc", Title: "T", Payload: json.RawMessage(`{"severity": "high"}`), SourceEvent: "k1", Kind: "advisory", ActorID: a.ID}

	w1, out, err := s.RecordSourced(ctx, in)
	if err != nil || out != OutcomeNew {
		t.Fatalf("first: %v %v", out, err)
	}
	// Same payload, different spacing: nothing changes.
	in.Payload = json.RawMessage(`{"severity":"high"}`)
	w2, out, err := s.RecordSourced(ctx, in)
	if err != nil || out != OutcomeSeen || w2.ID != w1.ID {
		t.Fatalf("repeat: %v %v same=%v", out, err, w2.ID == w1.ID)
	}
	in.Payload = json.RawMessage(`{"severity":"critical"}`)
	w3, out, err := s.RecordSourced(ctx, in)
	if err != nil || out != OutcomeChanged || w3.ID != w1.ID {
		t.Fatalf("change: %v %v", out, err)
	}
	if string(w3.Payload) != `{"severity":"critical"}` {
		t.Fatalf("payload = %s", w3.Payload)
	}
	if len(w3.Refs) != 1 || w3.Refs[0].SourceEvent != "k1" {
		t.Fatalf("refs = %+v", w3.Refs)
	}
	ev, err := s.WorkItemEvents(ctx, w1.ID)
	if err != nil || len(ev) != 2 || ev[0].Kind != "advisory.created" || ev[1].Kind != "advisory.changed" {
		t.Fatalf("events = %+v, %v", ev, err)
	}
	if !strings.Contains(string(ev[1].Data), `"from":{"severity": "high"}`) && !strings.Contains(string(ev[1].Data), `"from":{"severity":"high"}`) {
		t.Fatalf("change event = %s", ev[1].Data)
	}
	items, _ := s.WorkItemsByProject(ctx, p.ID)
	if len(items) != 1 {
		t.Fatalf("%d work items, want 1", len(items))
	}
}

func TestRecordSourcedKeepsAReferenceInOneProject(t *testing.T) {
	ctx := context.Background()
	s, p, a := sourcedFixture(t)
	other, _, err := s.UpsertProject(ctx, "default", "acme/lib", 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	in := Sourced{ProjectID: p.ID, Ref: "advisory:CVE-2024-1", Payload: json.RawMessage(`{}`), Kind: "advisory", ActorID: a.ID}
	if _, _, err := s.RecordSourced(ctx, in); err != nil {
		t.Fatal(err)
	}
	in.ProjectID = other.ID
	if _, _, err := s.RecordSourced(ctx, in); !errors.Is(err, ErrWrongProject) {
		t.Fatalf("err = %v, want ErrWrongProject", err)
	}
	items, _ := s.WorkItemsByProject(ctx, other.ID)
	if len(items) != 0 {
		t.Fatal("the other project got a work item")
	}
}

func TestRecordSourcedRefusesBadInput(t *testing.T) {
	ctx := context.Background()
	s, p, a := sourcedFixture(t)
	for _, in := range []Sourced{
		{ProjectID: p.ID, Ref: "no spaces allowed", Payload: json.RawMessage(`{}`), ActorID: a.ID},
		{ProjectID: p.ID, Ref: "advisory:CVE-2024-2", Payload: json.RawMessage(`{`), ActorID: a.ID},
	} {
		if _, _, err := s.RecordSourced(ctx, in); err == nil {
			t.Fatalf("accepted %+v", in)
		}
	}
}
