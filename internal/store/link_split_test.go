package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSplitLinkMovesExactlyTheMovedReferencesBack(t *testing.T) {
	ctx := context.Background()
	f := newLinkFix(t)
	// a1 already held a reference before the accept: it must stay on a1.
	if _, err := f.s.AttachRef(ctx, f.a1.ID, "branch:acme/app:fix", "ev", f.a.ID); err != nil {
		t.Fatal(err)
	}
	p := f.propose(t, f.a1.ID, f.a2.ID)
	if _, err := f.s.AcceptLink(ctx, p.ID, "ada"); err != nil {
		t.Fatal(err)
	}
	before, _ := f.s.workItem(ctx, f.a2.ID)
	if before.State != "merged" {
		t.Fatalf("not merged: %+v", before)
	}
	got, err := f.s.SplitLink(ctx, p.ID, "grace")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "split" || got.SplitBy != "grace" || got.SplitAt.IsZero() || got.DecidedBy != "ada" {
		t.Errorf("proposal = %+v", got)
	}
	first, _ := f.s.workItem(ctx, f.a1.ID)
	second, _ := f.s.workItem(ctx, f.a2.ID)
	if len(first.Refs) != 2 || len(second.Refs) != 2 || second.State != "open" {
		t.Fatalf("first %+v second %+v", first.Refs, second)
	}
	if it, _ := f.s.linkItem(ctx, f.a2.ID); it.MergedInto != "" {
		t.Errorf("merged_into = %q", it.MergedInto)
	}
	for _, id := range []string{f.a1.ID, f.a2.ID} {
		ev, _ := f.s.WorkItemEvents(ctx, id)
		last := ev[len(ev)-1]
		if last.Kind != "link.split" || !strings.Contains(string(last.Data), p.ID) || !strings.Contains(string(last.Data), "jira:ABC-3") {
			t.Errorf("events of %s = %+v", id, ev)
		}
	}
	// A second split is refused, and the pair may be proposed again.
	if _, err := f.s.SplitLink(ctx, p.ID, "grace"); !errors.Is(err, ErrLinkClosed) {
		t.Errorf("split again: %v", err)
	}
	f.propose(t, f.a1.ID, f.a2.ID)
}

func TestSplitLinkRestoresThePreviousStateAndOriginalAttribution(t *testing.T) {
	ctx := context.Background()
	f := newLinkFix(t)
	if _, err := f.s.db.ExecContext(ctx, `UPDATE work_item SET state = 'triaged' WHERE id = ?`, f.a2.ID); err != nil {
		t.Fatal(err)
	}
	var wasBy, wasAt string
	if err := f.s.db.QueryRowContext(ctx, `SELECT attached_by, attached_at FROM work_item_ref WHERE ref = 'jira:ABC-2'`).Scan(&wasBy, &wasAt); err != nil {
		t.Fatal(err)
	}
	p := f.propose(t, f.a1.ID, f.a2.ID)
	if _, err := f.s.AcceptLink(ctx, p.ID, "ada"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.SplitLink(ctx, p.ID, "grace"); err != nil {
		t.Fatal(err)
	}
	second, _ := f.s.workItem(ctx, f.a2.ID)
	if second.State != "triaged" {
		t.Errorf("state = %q", second.State)
	}
	var by, at string
	if err := f.s.db.QueryRowContext(ctx, `SELECT attached_by, attached_at FROM work_item_ref WHERE ref = 'jira:ABC-2'`).Scan(&by, &at); err != nil || by != wasBy || at != wasAt {
		t.Errorf("attribution %q %q, was %q %q (%v)", by, at, wasBy, wasAt, err)
	}
}

func TestSplitLinkRefusesAndChangesNothing(t *testing.T) {
	ctx := context.Background()
	check := func(name string, f linkFix, id string, want error) {
		t.Helper()
		if _, err := f.s.SplitLink(ctx, id, "grace"); !errors.Is(err, want) {
			t.Errorf("%s: err = %v, want %v", name, err, want)
		}
	}

	f := newLinkFix(t)
	open := f.propose(t, f.a1.ID, f.a2.ID)
	check("open proposal", f, open.ID, ErrLinkClosed)
	check("unknown proposal", f, "lp_missing", ErrNotFound)
	if _, err := f.s.SplitLink(ctx, open.ID, " "); !errors.Is(err, ErrLinkInvalid) {
		t.Errorf("no actor: %v", err)
	}
	if _, err := f.s.DeclineLink(ctx, open.ID, "ada"); err != nil {
		t.Fatal(err)
	}
	check("declined proposal", f, open.ID, ErrLinkClosed)

	// A moved reference was detached from the kept item.
	f = newLinkFix(t)
	p := f.propose(t, f.a1.ID, f.a2.ID)
	if _, err := f.s.AcceptLink(ctx, p.ID, "ada"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.db.ExecContext(ctx, `DELETE FROM work_item_ref WHERE ref = 'jira:ABC-3'`); err != nil {
		t.Fatal(err)
	}
	check("reference gone", f, p.ID, ErrLinkClosed)

	// A moved reference now belongs to a third item.
	f = newLinkFix(t)
	third, _ := f.s.CreateWorkItem(ctx, f.proj.ID, "third", f.a.ID, "sentry:3")
	p = f.propose(t, f.a1.ID, f.a2.ID)
	if _, err := f.s.AcceptLink(ctx, p.ID, "ada"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.db.ExecContext(ctx, `UPDATE work_item_ref SET work_item_id = ? WHERE ref = 'jira:ABC-2'`, third.ID); err != nil {
		t.Fatal(err)
	}
	check("reference moved elsewhere", f, p.ID, ErrLinkClosed)
	first, _ := f.s.workItem(ctx, f.a1.ID)
	second, _ := f.s.workItem(ctx, f.a2.ID)
	if len(first.Refs) != 2 || second.State != "merged" || len(second.Refs) != 0 {
		t.Errorf("a refused split changed something: %+v %+v", first.Refs, second)
	}
	if got, _ := f.s.LinkProposalByID(ctx, p.ID); got.State != "accepted" {
		t.Errorf("state = %s", got.State)
	}
	ev, _ := f.s.WorkItemEvents(ctx, f.a1.ID)
	if ev[len(ev)-1].Kind != "link.accepted" {
		t.Errorf("a refused split wrote an event: %+v", ev)
	}

	// The second item was merged again elsewhere, or the pointer is gone.
	f = newLinkFix(t)
	p = f.propose(t, f.a1.ID, f.a2.ID)
	if _, err := f.s.AcceptLink(ctx, p.ID, "ada"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.db.ExecContext(ctx, `UPDATE work_item SET state = 'open' WHERE id = ?`, f.a2.ID); err != nil {
		t.Fatal(err)
	}
	check("second item no longer merged", f, p.ID, ErrLinkClosed)

	// A live claim on the kept item blocks the split.
	f = newLinkFix(t)
	p = f.propose(t, f.a1.ID, f.a2.ID)
	if _, err := f.s.AcceptLink(ctx, p.ID, "ada"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.TakeClaim(ctx, f.a1.ID, f.a.ID, "laptop", "fix", time.Hour); err != nil {
		t.Fatal(err)
	}
	check("claimed", f, p.ID, ErrLinkClosed)
}

func TestMigration7RebuildsLinkProposalAndKeepsItsRows(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "dots.db")
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`CREATE TABLE schema_version (version INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for i := range 6 {
		if _, err := raw.Exec(migrations[i]); err != nil {
			t.Fatalf("migration %d: %v", i+1, err)
		}
		raw.Exec(`INSERT INTO schema_version (version) VALUES (?)`, i+1)
	}
	for _, q := range []string{
		`INSERT INTO workspace (name) VALUES ('default')`,
		`INSERT INTO actor (id, kind, display_name, created_at) VALUES ('ac_1', 'human', 'Ada', 't')`,
		`INSERT INTO project (id, workspace, repo, created_at, updated_at) VALUES ('pr_1', 'default', 'acme/app', 't', 't')`,
		`INSERT INTO work_item (id, project_id, created_by, created_at, updated_at) VALUES ('wi_1', 'pr_1', 'ac_1', 't', 't'), ('wi_2', 'pr_1', 'ac_1', 't', 't')`,
		`INSERT INTO link_proposal (id, work_item_a, work_item_b, confidence, reason, state, proposed_by, created_at, decided_by, decided_at)
			VALUES ('lp_1', 'wi_1', 'wi_2', 0.7, 'why', 'declined', 'bot', 't0', 'ada', 't1'),
			       ('lp_2', 'wi_1', 'wi_2', 0.9, 'again', 'open', 'bot', 't2', '', '')`,
	} {
		if _, err := raw.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	raw.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if v, _ := s.schemaVersion(ctx); v != len(migrations) || v < 7 {
		t.Errorf("schema version = %d", v)
	}
	p, err := s.LinkProposalByID(ctx, "lp_1")
	if err != nil || p.State != "declined" || p.Reason != "why" || p.DecidedBy != "ada" || p.SplitBy != "" {
		t.Errorf("lp_1 = %+v, %v", p, err)
	}
	// The new state is accepted by the table, and the open-pair index still holds.
	if _, err := s.db.ExecContext(ctx, `UPDATE link_proposal SET state = 'split' WHERE id = 'lp_1'`); err != nil {
		t.Errorf("split state refused: %v", err)
	}
	if _, err := s.ProposeLink(ctx, LinkInput{A: "wi_2", B: "wi_1", Confidence: 0.5, ProposedBy: "bot"}); !errors.Is(err, ErrLinkExists) {
		t.Errorf("open pair index lost: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE link_proposal SET state = 'bogus' WHERE id = 'lp_1'`); err == nil {
		t.Error("the CHECK accepts any state")
	}
}
