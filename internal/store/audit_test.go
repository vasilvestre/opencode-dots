package store

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestAuditIsStampedByTheServerAndListedNewestFirst(t *testing.T) {
	s, ctx := open(t), context.Background()
	at := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	for i, target := range []string{"a.yaml", "b.yaml", "c.yaml"} {
		now := at.Add(time.Duration(i) * time.Minute)
		s.clock = func() time.Time { return now }
		if err := s.RecordAudit(ctx, "PUT /api/save", target, "local", 200); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.RecentAudit(ctx, 2)
	if err != nil || len(got) != 2 {
		t.Fatalf("recent = %v, %v", got, err)
	}
	if got[0].Target != "c.yaml" || got[1].Target != "b.yaml" || !got[0].At.Equal(at.Add(2*time.Minute)) || got[0].Outcome != 200 {
		t.Errorf("entries = %+v", got)
	}
}

func TestAuditRefusesAnUpdateAndADelete(t *testing.T) {
	s, ctx := open(t), context.Background()
	if err := s.RecordAudit(ctx, "PUT /api/save", "a.yaml", "local", 200); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE audit SET caller = 'x'`); err == nil {
		t.Error("an audit row was updated")
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM audit`); err == nil {
		t.Error("an audit row was deleted")
	}
	if got, _ := s.RecentAudit(ctx, 10); len(got) != 1 || got[0].Caller != "local" {
		t.Errorf("entries = %+v", got)
	}
}

func TestAuditLimitsAndFields(t *testing.T) {
	s, ctx := open(t), context.Background()
	if err := s.RecordAudit(ctx, "", "", "local", 200); err == nil {
		t.Error("an entry without an endpoint was accepted")
	}
	s.RecordAudit(ctx, "PUT /api/save", "a\nb"+strings.Repeat("x", 500), "ada@example.com", 403)
	got, _ := s.RecentAudit(ctx, 100000)
	if len(got) != 1 || strings.ContainsAny(got[0].Target, "\n") || len(got[0].Target) != maxAuditField {
		t.Errorf("entry = %+v", got)
	}
}
