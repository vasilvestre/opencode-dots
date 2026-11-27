package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// SplitLink undoes an accepted link (ADR 0041): the references the accept moved go back to
// the second work item, which takes its earlier state again, and the proposal becomes
// `split`. What moved is read from the `link.accepted` event of the kept item, which holds
// each reference as it was. It refuses, changing nothing, unless the proposal is accepted,
// the second item is still `merged` into the first, every moved reference is still
// attached to the first, and the first holds no live claim (someone works on the whole).
// Both items get a `link.split` event. Everything happens in one transaction.
func (s *Store) SplitLink(ctx context.Context, id, by string) (LinkProposal, error) {
	by = CleanLinkText(by, maxLinkActor)
	if by == "" {
		return LinkProposal{}, fmt.Errorf("%w: a split names who took it", ErrLinkInvalid)
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
	if p.State != "accepted" {
		return LinkProposal{}, fmt.Errorf("%w: only an accepted link can be split, this one is %s", ErrLinkClosed, p.State)
	}
	var merged, mergedInto string
	if err := tx.QueryRowContext(ctx, `SELECT state, merged_into FROM work_item WHERE id = ?`, p.B).Scan(&merged, &mergedInto); err != nil {
		return LinkProposal{}, err
	}
	if merged != "merged" || mergedInto != p.A {
		return LinkProposal{}, fmt.Errorf("%w: work item %s is no longer merged into %s", ErrLinkClosed, p.B, p.A)
	}
	moved, previous, err := acceptedMove(ctx, tx, p)
	if err != nil {
		return LinkProposal{}, err
	}
	for _, m := range moved {
		var holder string
		err := tx.QueryRowContext(ctx, `SELECT work_item_id FROM work_item_ref WHERE ref = ?`, m.Ref).Scan(&holder)
		if errors.Is(err, sql.ErrNoRows) {
			return LinkProposal{}, fmt.Errorf("%w: reference %s no longer exists", ErrLinkClosed, m.Ref)
		}
		if err != nil {
			return LinkProposal{}, err
		}
		if holder != p.A {
			return LinkProposal{}, fmt.Errorf("%w: reference %s is no longer attached to %s", ErrLinkClosed, m.Ref, p.A)
		}
	}
	at := stamp(s.clock())
	var live int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM claim WHERE work_item_id = ? AND released_at = '' AND expires_at > ?`, p.A, at).Scan(&live); err != nil {
		return LinkProposal{}, err
	}
	if live > 0 {
		return LinkProposal{}, fmt.Errorf("%w: the kept work item holds a live claim", ErrLinkClosed)
	}
	for _, m := range moved {
		if _, err := tx.ExecContext(ctx, `UPDATE work_item_ref SET work_item_id = ? WHERE ref = ? AND work_item_id = ?`, p.B, m.Ref, p.A); err != nil {
			return LinkProposal{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE work_item SET state = ?, merged_into = '', updated_at = ? WHERE id = ?`, previous, at, p.B); err != nil {
		return LinkProposal{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE work_item SET updated_at = ? WHERE id = ?`, at, p.A); err != nil {
		return LinkProposal{}, err
	}
	onB, _ := json.Marshal(map[string]any{"proposal": p.ID, "from": p.A, "refs": moved, "restored_state": previous})
	onA, _ := json.Marshal(map[string]any{"proposal": p.ID, "into": p.B, "refs": moved, "restored_state": previous})
	if err := addEvent(ctx, tx, p.B, "link.split", actor.ID, at, onB); err != nil {
		return LinkProposal{}, err
	}
	if err := addEvent(ctx, tx, p.A, "link.split", actor.ID, at, onA); err != nil {
		return LinkProposal{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE link_proposal SET state = 'split', split_by = ?, split_at = ? WHERE id = ?`, by, at, p.ID); err != nil {
		return LinkProposal{}, err
	}
	if err := tx.Commit(); err != nil {
		return LinkProposal{}, err
	}
	return s.LinkProposalByID(ctx, p.ID)
}

// acceptedMove reads what the accept of a proposal moved: the references and the state the
// second item had, from the `link.accepted` event of the first.
func acceptedMove(ctx context.Context, tx *sql.Tx, p LinkProposal) ([]movedRef, string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT data FROM work_item_event WHERE work_item_id = ? AND kind = 'link.accepted' ORDER BY id`, p.A)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, "", err
		}
		var ev struct {
			Proposal string     `json:"proposal"`
			Refs     []movedRef `json:"refs"`
			Previous string     `json:"previous_state"`
		}
		if json.Unmarshal([]byte(data), &ev) != nil || ev.Proposal != p.ID {
			continue
		}
		if ev.Previous == "" || ev.Previous == "merged" {
			ev.Previous = "open"
		}
		return ev.Refs, ev.Previous, nil
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	return nil, "", fmt.Errorf("%w: the accept of %s left no record of what moved", ErrLinkClosed, p.ID)
}
