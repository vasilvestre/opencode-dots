package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// Outcome of recording what a source said about a reference.
type Outcome string

const (
	// OutcomeNew means no work item held the reference: one was created.
	OutcomeNew Outcome = "new"
	// OutcomeSeen means the work item exists and the payload is the one it already has.
	OutcomeSeen Outcome = "seen"
	// OutcomeChanged means the work item exists and the payload differs: it was replaced
	// and an event recorded.
	OutcomeChanged Outcome = "changed"
	// OutcomeRefused is not returned by RecordSourced, which fails with ErrWrongProject; callers
	// that go on with the next report use it for the line they print.
	OutcomeRefused Outcome = "refused"
)

// ErrWrongProject is returned when a reference already belongs to a work item of another
// project. A reference names one work item (ADR 0023): nothing is moved or merged here.
var ErrWrongProject = errors.New("store: reference belongs to a work item of another project")

// Sourced is one thing a source reports: a reference, a title and a payload.
type Sourced struct {
	ProjectID string
	Ref       string // tracker:key, from a structured field of the source
	// LegacyRef is an earlier form of Ref. When no item holds Ref but an item of the same
	// project holds LegacyRef, that item is the one: its reference is rewritten to Ref and an
	// "<kind>.ref_migrated" event is recorded. An item of another project is not touched.
	LegacyRef string
	Title     string          // data from outside
	Payload   json.RawMessage // data from outside; must be valid JSON
	// SourceEvent is the source's idempotency key, kept on the reference of a new item.
	SourceEvent string
	// Kind names the events recorded on a new item or a change, such as "advisory".
	Kind    string
	ActorID string
}

// RecordSourced creates the work item for a reference the first time, and afterwards
// finds it again. A repeat with the same payload writes nothing. A different payload
// replaces the stored one and appends an event holding the previous and the new payload,
// so a poll that finds a changed severity leaves a trail. Everything happens in one
// transaction. The reference is never taken from the payload or the title.
func (s *Store) RecordSourced(ctx context.Context, in Sourced) (WorkItem, Outcome, error) {
	if err := checkRef(in.Ref); err != nil {
		return WorkItem{}, "", err
	}
	if !json.Valid(in.Payload) {
		return WorkItem{}, "", fmt.Errorf("store: payload of %s is not JSON", in.Ref)
	}
	var fresh bytes.Buffer
	if err := json.Compact(&fresh, in.Payload); err != nil {
		return WorkItem{}, "", err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return WorkItem{}, "", err
	}
	defer tx.Rollback()

	var id, project, stored string
	err = tx.QueryRowContext(ctx, `
		SELECT w.id, w.project_id, w.payload FROM work_item w
		JOIN work_item_ref r ON r.work_item_id = w.id WHERE r.ref = ?`, in.Ref).Scan(&id, &project, &stored)
	stamp := now()
	outcome := OutcomeSeen
	if errors.Is(err, sql.ErrNoRows) && in.LegacyRef != "" {
		var oldID, oldProject, oldPayload string
		lerr := tx.QueryRowContext(ctx, `
			SELECT w.id, w.project_id, w.payload FROM work_item w
			JOIN work_item_ref r ON r.work_item_id = w.id WHERE r.ref = ?`, in.LegacyRef).Scan(&oldID, &oldProject, &oldPayload)
		switch {
		case errors.Is(lerr, sql.ErrNoRows) || (lerr == nil && oldProject != in.ProjectID):
			// Nothing to migrate: create the item below.
		case lerr != nil:
			return WorkItem{}, "", lerr
		default:
			if _, err := tx.ExecContext(ctx, `UPDATE work_item_ref SET ref = ?, source_event = ? WHERE ref = ?`, in.Ref, in.SourceEvent, in.LegacyRef); err != nil {
				return WorkItem{}, "", err
			}
			move, merr := json.Marshal(map[string]string{"from": in.LegacyRef, "to": in.Ref})
			if merr != nil {
				return WorkItem{}, "", merr
			}
			if err := addEvent(ctx, tx, oldID, in.Kind+".ref_migrated", in.ActorID, stamp, move); err != nil {
				return WorkItem{}, "", err
			}
			id, project, stored, err = oldID, oldProject, oldPayload, nil
		}
	}
	switch {
	case errors.Is(err, sql.ErrNoRows):
		id = newID("wi")
		if _, err := tx.ExecContext(ctx, `INSERT INTO work_item (id, project_id, title, state, payload, created_by, created_at, updated_at) VALUES (?, ?, ?, 'open', ?, ?, ?, ?)`,
			id, in.ProjectID, in.Title, fresh.String(), in.ActorID, stamp, stamp); err != nil {
			return WorkItem{}, "", err
		}
		if err := attach(ctx, tx, id, in.Ref, in.SourceEvent, in.ActorID, stamp); err != nil {
			return WorkItem{}, "", err
		}
		if err := addEvent(ctx, tx, id, in.Kind+".created", in.ActorID, stamp, fresh.Bytes()); err != nil {
			return WorkItem{}, "", err
		}
		outcome = OutcomeNew
	case err != nil:
		return WorkItem{}, "", err
	case project != in.ProjectID:
		return WorkItem{}, "", fmt.Errorf("%w: %s", ErrWrongProject, in.Ref)
	default:
		var old bytes.Buffer
		if json.Compact(&old, []byte(stored)) != nil || !bytes.Equal(old.Bytes(), fresh.Bytes()) {
			if _, err := tx.ExecContext(ctx, `UPDATE work_item SET payload = ?, title = ?, updated_at = ? WHERE id = ?`,
				fresh.String(), in.Title, stamp, id); err != nil {
				return WorkItem{}, "", err
			}
			change, err := json.Marshal(map[string]json.RawMessage{"from": json.RawMessage(stored), "to": fresh.Bytes()})
			if err != nil {
				return WorkItem{}, "", err
			}
			if err := addEvent(ctx, tx, id, in.Kind+".changed", in.ActorID, stamp, change); err != nil {
				return WorkItem{}, "", err
			}
			outcome = OutcomeChanged
		}
	}
	if err := tx.Commit(); err != nil {
		return WorkItem{}, "", err
	}
	w, err := s.workItem(ctx, id)
	return w, outcome, err
}

func addEvent(ctx context.Context, tx execer, workItemID, kind, actorID, stamp string, data []byte) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO work_item_event (work_item_id, at, kind, actor_id, data) VALUES (?, ?, ?, ?, ?)`,
		workItemID, stamp, kind, actorID, string(data))
	return err
}

// Event is one entry of a work item's trail.
type Event struct {
	At      string
	Kind    string
	ActorID string
	Data    json.RawMessage
}

// WorkItemEvents returns the trail of a work item, oldest first.
func (s *Store) WorkItemEvents(ctx context.Context, workItemID string) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT at, kind, actor_id, data FROM work_item_event WHERE work_item_id = ? ORDER BY id`, workItemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		var data string
		if err := rows.Scan(&e.At, &e.Kind, &e.ActorID, &data); err != nil {
			return nil, err
		}
		e.Data = json.RawMessage(data)
		out = append(out, e)
	}
	return out, rows.Err()
}
