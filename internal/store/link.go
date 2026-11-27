package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode"
)

// Link proposals (ADR 0041, first step). A proposal names two work items, a confidence, a
// reason and the evidence a workflow used. It is data: nothing changes until a person
// accepts it. Accepting is one explicit action (ADR 0023): the references of the second
// item move onto the first, and the second is kept as `merged`, with a pointer to the first
// and the references it held written in the events of both, so a later split can restore it.

var (
	// ErrLinkExists is returned when the pair already has an open proposal, in either order.
	ErrLinkExists = errors.New("store: this pair already has an open link proposal")
	// ErrLinkClosed is returned when a proposal is decided already, or names a merged item.
	ErrLinkClosed = errors.New("store: the link proposal or one of its work items is closed")
	// ErrLinkBoundary is returned when the two items are in different workspaces or under
	// different trust levels (ADR 0032). A link never carries content across that boundary.
	ErrLinkBoundary = errors.New("store: the work items sit on different sides of a trust boundary")
	// ErrLinkInvalid is returned for a proposal that is not well formed.
	ErrLinkInvalid = errors.New("store: invalid link proposal")
)

// Limits of a proposal: the reason is one short paragraph, the evidence a small JSON object.
const (
	MaxLinkReason   = 500
	MaxLinkEvidence = 8 << 10
	maxLinkActor    = 80
	// DefaultLinkLimit and MaxLinkLimit bound ListLinkProposals.
	DefaultLinkLimit = 100
	MaxLinkLimit     = 500
)

// LinkItem is one side of a proposal, as the list shows it.
type LinkItem struct {
	ID         string
	Title      string
	State      string
	Workspace  string
	Repo       string
	Trust      string
	Refs       []string
	MergedInto string
}

// LinkProposal is a proposal to treat two work items as one piece of work.
type LinkProposal struct {
	ID         string
	A, B       string // work item ids; accepting moves the references of B onto A
	Confidence float64
	Reason     string // data from outside
	Evidence   json.RawMessage
	State      string // open, accepted or declined
	ProposedBy string
	CreatedAt  time.Time
	DecidedBy  string
	DecidedAt  time.Time
	ItemA      LinkItem
	ItemB      LinkItem
}

// LinkInput is what a workflow proposes.
type LinkInput struct {
	A, B       string
	Confidence float64
	Reason     string
	Evidence   json.RawMessage
	ProposedBy string
}

// CleanLinkText makes text from outside fit for a record: one paragraph, no control
// characters, at most max runes. The text is data and is never read as an instruction.
func CleanLinkText(s string, max int) string {
	s = strings.Join(strings.FieldsFunc(s, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }), " ")
	if r := []rune(s); len(r) > max {
		s = strings.TrimSpace(string(r[:max]))
	}
	return s
}

// ProposeLink records an open proposal and an event on both work items.
func (s *Store) ProposeLink(ctx context.Context, in LinkInput) (LinkProposal, error) {
	if in.A == "" || in.B == "" || in.A == in.B {
		return LinkProposal{}, fmt.Errorf("%w: two different work items are needed", ErrLinkInvalid)
	}
	if math.IsNaN(in.Confidence) || in.Confidence < 0 || in.Confidence > 1 {
		return LinkProposal{}, fmt.Errorf("%w: the confidence is between 0 and 1", ErrLinkInvalid)
	}
	by := CleanLinkText(in.ProposedBy, maxLinkActor)
	if by == "" {
		return LinkProposal{}, fmt.Errorf("%w: a proposal names who proposed it", ErrLinkInvalid)
	}
	evidence := []byte(`{}`)
	if len(bytes.TrimSpace(in.Evidence)) > 0 {
		var obj map[string]json.RawMessage
		if len(in.Evidence) > MaxLinkEvidence || json.Unmarshal(in.Evidence, &obj) != nil || obj == nil {
			return LinkProposal{}, fmt.Errorf("%w: the evidence is a JSON object of at most %d bytes", ErrLinkInvalid, MaxLinkEvidence)
		}
		var buf bytes.Buffer
		if err := json.Compact(&buf, in.Evidence); err != nil {
			return LinkProposal{}, err
		}
		evidence = buf.Bytes()
	}
	actor, err := s.EnsureActor(ctx, Actor{Kind: "bot", DisplayName: by, Identities: map[string]string{"dots": by}})
	if err != nil {
		return LinkProposal{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LinkProposal{}, err
	}
	defer tx.Rollback()
	for _, id := range []string{in.A, in.B} {
		info, err := linkInfo(ctx, tx, id)
		if err != nil {
			return LinkProposal{}, err
		}
		if info.state == "merged" {
			return LinkProposal{}, fmt.Errorf("%w: work item %s is merged", ErrLinkClosed, id)
		}
	}
	id, at := newID("lp"), stamp(s.clock())
	_, err = tx.ExecContext(ctx, `INSERT INTO link_proposal (id, work_item_a, work_item_b, confidence, reason, evidence, state, proposed_by, created_at)
		VALUES (?, ?, ?, ?, ?, ?, 'open', ?, ?)`, id, in.A, in.B, in.Confidence, CleanLinkText(in.Reason, MaxLinkReason), string(evidence), by, at)
	if isUnique(err) {
		return LinkProposal{}, ErrLinkExists
	}
	if err != nil {
		return LinkProposal{}, err
	}
	data, _ := json.Marshal(map[string]any{"proposal": id, "a": in.A, "b": in.B, "confidence": in.Confidence})
	for _, w := range []string{in.A, in.B} {
		if err := addEvent(ctx, tx, w, "link.proposed", actor.ID, at, data); err != nil {
			return LinkProposal{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return LinkProposal{}, err
	}
	return s.LinkProposalByID(ctx, id)
}

const linkColumns = `id, work_item_a, work_item_b, confidence, reason, evidence, state, proposed_by, created_at, decided_by, decided_at`

func scanLink(r scanner) (LinkProposal, error) {
	var (
		p                          LinkProposal
		evidence, created, decided string
	)
	if err := r.Scan(&p.ID, &p.A, &p.B, &p.Confidence, &p.Reason, &evidence, &p.State, &p.ProposedBy, &created, &p.DecidedBy, &decided); err != nil {
		return LinkProposal{}, err
	}
	p.Evidence = json.RawMessage(evidence)
	p.CreatedAt, p.DecidedAt = parseTime(created), parseTime(decided)
	return p, nil
}

// LinkProposalByID returns one proposal with both items filled in, or ErrNotFound.
func (s *Store) LinkProposalByID(ctx context.Context, id string) (LinkProposal, error) {
	p, err := scanLink(s.db.QueryRowContext(ctx, `SELECT `+linkColumns+` FROM link_proposal WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return LinkProposal{}, ErrNotFound
	}
	if err != nil {
		return LinkProposal{}, err
	}
	return s.fillLink(ctx, p)
}

func (s *Store) fillLink(ctx context.Context, p LinkProposal) (LinkProposal, error) {
	var err error
	if p.ItemA, err = s.linkItem(ctx, p.A); err != nil {
		return p, err
	}
	p.ItemB, err = s.linkItem(ctx, p.B)
	return p, err
}

func (s *Store) linkItem(ctx context.Context, id string) (LinkItem, error) {
	it := LinkItem{ID: id}
	var features string
	err := s.db.QueryRowContext(ctx, `
		SELECT w.title, w.state, w.merged_into, p.workspace, p.repo, p.features
		FROM work_item w JOIN project p ON p.id = w.project_id WHERE w.id = ?`, id).
		Scan(&it.Title, &it.State, &it.MergedInto, &it.Workspace, &it.Repo, &features)
	if errors.Is(err, sql.ErrNoRows) {
		return it, ErrNotFound
	}
	if err != nil {
		return it, err
	}
	it.Trust = trustOf(features)
	rows, err := s.db.QueryContext(ctx, `SELECT ref FROM work_item_ref WHERE work_item_id = ? ORDER BY attached_at, ref`, id)
	if err != nil {
		return it, err
	}
	defer rows.Close()
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			return it, err
		}
		it.Refs = append(it.Refs, ref)
	}
	return it, rows.Err()
}

// trustOf reads the trust level out of a project's stored features. Absent means open,
// the default of ADR 0032.
func trustOf(features string) string {
	var f struct {
		Trust string `json:"trust"`
	}
	if json.Unmarshal([]byte(features), &f) != nil || f.Trust == "" {
		return "open"
	}
	return f.Trust
}

// ListLinkProposals returns the proposals, open and decided, newest first. A limit outside
// 1..MaxLinkLimit takes the default or the maximum.
func (s *Store) ListLinkProposals(ctx context.Context, limit int) ([]LinkProposal, error) {
	switch {
	case limit <= 0:
		limit = DefaultLinkLimit
	case limit > MaxLinkLimit:
		limit = MaxLinkLimit
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+linkColumns+` FROM link_proposal ORDER BY created_at DESC, rowid DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	var out []LinkProposal
	for rows.Next() {
		p, err := scanLink(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for i := range out {
		if out[i], err = s.fillLink(ctx, out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// DeclineLink closes an open proposal as declined and records an event on both items. The
// work items are not touched.
func (s *Store) DeclineLink(ctx context.Context, id, by string) (LinkProposal, error) {
	return s.decideLink(ctx, id, by, false)
}

// AcceptLink moves the references of the second work item onto the first, marks the second
// `merged` with a pointer to the first, and records events on both, in one transaction.
// It refuses, leaving the proposal open, when the items are in different workspaces or
// under different trust levels (ErrLinkBoundary), when one is already merged, or when the
// second holds a live claim. A link across a boundary would carry references and state
// only, and that is not built: it is refused.
func (s *Store) AcceptLink(ctx context.Context, id, by string) (LinkProposal, error) {
	return s.decideLink(ctx, id, by, true)
}

func (s *Store) decideLink(ctx context.Context, id, by string, accept bool) (LinkProposal, error) {
	by = CleanLinkText(by, maxLinkActor)
	if by == "" {
		return LinkProposal{}, fmt.Errorf("%w: a decision names who took it", ErrLinkInvalid)
	}
	actor, err := s.EnsureActor(ctx, Actor{Kind: "human", DisplayName: by, Identities: map[string]string{"dots": by}})
	if err != nil {
		return LinkProposal{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LinkProposal{}, err
	}
	defer tx.Rollback()
	p, err := scanLink(tx.QueryRowContext(ctx, `SELECT `+linkColumns+` FROM link_proposal WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return LinkProposal{}, ErrNotFound
	}
	if err != nil {
		return LinkProposal{}, err
	}
	if p.State != "open" {
		return LinkProposal{}, fmt.Errorf("%w: it is already %s", ErrLinkClosed, p.State)
	}
	at := stamp(s.clock())
	state := "declined"
	if accept {
		state = "accepted"
		if err := mergeItems(ctx, tx, p, actor.ID, at); err != nil {
			return LinkProposal{}, err
		}
	} else {
		data, _ := json.Marshal(map[string]string{"proposal": p.ID})
		for _, w := range []string{p.A, p.B} {
			if err := addEvent(ctx, tx, w, "link.declined", actor.ID, at, data); err != nil {
				return LinkProposal{}, err
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE link_proposal SET state = ?, decided_by = ?, decided_at = ? WHERE id = ?`, state, by, at, p.ID); err != nil {
		return LinkProposal{}, err
	}
	if err := tx.Commit(); err != nil {
		return LinkProposal{}, err
	}
	return s.LinkProposalByID(ctx, p.ID)
}

type linkInfoRow struct {
	state, workspace, trust string
}

type rowQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func linkInfo(ctx context.Context, q rowQuerier, id string) (linkInfoRow, error) {
	var r linkInfoRow
	var features string
	err := q.QueryRowContext(ctx, `
		SELECT w.state, p.workspace, p.features FROM work_item w JOIN project p ON p.id = w.project_id WHERE w.id = ?`, id).
		Scan(&r.state, &r.workspace, &features)
	if errors.Is(err, sql.ErrNoRows) {
		return r, fmt.Errorf("%w: work item %s", ErrNotFound, id)
	}
	r.trust = trustOf(features)
	return r, err
}

// movedRef is a reference as it was before it moved, kept in the events so a split can put
// it back with its original attribution.
type movedRef struct {
	Ref         string `json:"ref"`
	SourceEvent string `json:"source_event"`
	AttachedBy  string `json:"attached_by"`
	AttachedAt  string `json:"attached_at"`
}

func mergeItems(ctx context.Context, tx *sql.Tx, p LinkProposal, actorID, at string) error {
	a, err := linkInfo(ctx, tx, p.A)
	if err != nil {
		return err
	}
	b, err := linkInfo(ctx, tx, p.B)
	if err != nil {
		return err
	}
	if a.state == "merged" || b.state == "merged" {
		return fmt.Errorf("%w: a work item is already merged", ErrLinkClosed)
	}
	if a.workspace != b.workspace {
		return fmt.Errorf("%w: workspaces %q and %q differ", ErrLinkBoundary, a.workspace, b.workspace)
	}
	if a.trust != b.trust {
		return fmt.Errorf("%w: trust levels %q and %q differ", ErrLinkBoundary, a.trust, b.trust)
	}
	var live int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM claim WHERE work_item_id = ? AND released_at = '' AND expires_at > ?`, p.B, at).Scan(&live); err != nil {
		return err
	}
	if live > 0 {
		return fmt.Errorf("%w: the second work item holds a live claim", ErrLinkClosed)
	}
	rows, err := tx.QueryContext(ctx, `SELECT ref, source_event, attached_by, attached_at FROM work_item_ref WHERE work_item_id = ? ORDER BY attached_at, ref`, p.B)
	if err != nil {
		return err
	}
	moved := []movedRef{}
	for rows.Next() {
		var m movedRef
		if err := rows.Scan(&m.Ref, &m.SourceEvent, &m.AttachedBy, &m.AttachedAt); err != nil {
			rows.Close()
			return err
		}
		moved = append(moved, m)
	}
	rows.Close()
	if _, err := tx.ExecContext(ctx, `UPDATE work_item_ref SET work_item_id = ? WHERE work_item_id = ?`, p.A, p.B); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE work_item SET state = 'merged', merged_into = ?, updated_at = ? WHERE id = ?`, p.A, at, p.B); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE work_item SET updated_at = ? WHERE id = ?`, at, p.A); err != nil {
		return err
	}
	into, _ := json.Marshal(map[string]any{"proposal": p.ID, "from": p.B, "refs": moved, "previous_state": b.state})
	from, _ := json.Marshal(map[string]any{"proposal": p.ID, "into": p.A, "refs": moved, "previous_state": b.state})
	if err := addEvent(ctx, tx, p.A, "link.accepted", actorID, at, into); err != nil {
		return err
	}
	return addEvent(ctx, tx, p.B, "link.merged", actorID, at, from)
}
