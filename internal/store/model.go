package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	sqlite "modernc.org/sqlite"
)

// Actor is a person or a bot that acts. Every row that records a change names one
// (ADR 0023), also while there is a single user.
type Actor struct {
	ID          string
	Kind        string // "human" or "bot"
	DisplayName string
	// Identities maps a tracker to the login the actor has there, for example
	// {"github": "octocat"}.
	Identities map[string]string
}

// EnsureActor inserts the actor, or updates its name and identities when the id is known.
// With no ID it first looks for an actor that already carries one of its identities and
// returns that one unchanged, so the same login is never two actors; otherwise it gets a
// new ID.
func (s *Store) EnsureActor(ctx context.Context, a Actor) (Actor, error) {
	if a.ID == "" {
		for tracker, login := range a.Identities {
			found, err := s.actorByIdentity(ctx, tracker, login)
			if err == nil {
				return found, nil
			}
			if !errors.Is(err, ErrNotFound) {
				return a, err
			}
		}
	}
	if a.Kind != "human" && a.Kind != "bot" {
		return a, fmt.Errorf("store: actor kind %q is not human or bot", a.Kind)
	}
	if a.DisplayName == "" {
		return a, errors.New("store: an actor needs a display name")
	}
	if a.ID == "" {
		a.ID = newID("ac")
	}
	ids, err := json.Marshal(nonNilStrings(a.Identities))
	if err != nil {
		return a, err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO actor (id, kind, display_name, identities, created_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET kind = excluded.kind, display_name = excluded.display_name, identities = excluded.identities`,
		a.ID, a.Kind, a.DisplayName, string(ids), now())
	return a, err
}

func nonNilStrings(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

// actorByIdentity returns the actor that carries a login on a tracker, or ErrNotFound.
func (s *Store) actorByIdentity(ctx context.Context, tracker, login string) (Actor, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, kind, display_name, identities FROM actor WHERE json_extract(identities, '$.' || ?) = ?`, tracker, login)
	var a Actor
	var ids string
	err := row.Scan(&a.ID, &a.Kind, &a.DisplayName, &ids)
	if errors.Is(err, sql.ErrNoRows) {
		return Actor{}, ErrNotFound
	}
	if err != nil {
		return Actor{}, err
	}
	return a, json.Unmarshal([]byte(ids), &a.Identities)
}

// Workspace settings are the watcher knobs of repos.yaml (awf_url, state_path,
// max_per_run, executors), stored as the JSON the caller gives.

// SetWorkspaceSettings creates the workspace if needed and replaces its settings.
func (s *Store) SetWorkspaceSettings(ctx context.Context, workspace string, settings json.RawMessage) error {
	if workspace == "" {
		return errors.New("store: a workspace needs a name")
	}
	if len(settings) == 0 {
		settings = json.RawMessage(`{}`)
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO workspace (name, settings) VALUES (?, ?)
		ON CONFLICT(name) DO UPDATE SET settings = excluded.settings`, workspace, string(settings))
	return err
}

// WorkspaceSettings returns the stored settings, `{}` for a workspace that has none.
func (s *Store) WorkspaceSettings(ctx context.Context, workspace string) (json.RawMessage, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT settings FROM workspace WHERE name = ?`, workspace).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return json.RawMessage(`{}`), nil
	}
	return json.RawMessage(raw), err
}

// Project is one repository the dot works on, with what the dot does on it.
type Project struct {
	ID        string
	Workspace string
	Repo      string // owner/name
	Position  int    // order in the source file, so generated files stay stable
	// Features holds the resolved feature keys of ADR 0022, as the registered
	// features validate them.
	Features  map[string]any
	CreatedAt time.Time
	UpdatedAt time.Time
}

// UpsertProject stores one project, keyed by workspace and repository. It reports whether
// anything changed, so an import that finds nothing new writes nothing.
func (s *Store) UpsertProject(ctx context.Context, workspace, repo string, position int, features map[string]any) (Project, bool, error) {
	if !strings.Contains(repo, "/") || strings.ContainsAny(repo, " \t\n") {
		return Project{}, false, fmt.Errorf("store: %q is not an owner/name repository", repo)
	}
	raw, err := json.Marshal(nonNilAny(features))
	if err != nil {
		return Project{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Project{}, false, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO workspace (name) VALUES (?) ON CONFLICT(name) DO NOTHING`, workspace); err != nil {
		return Project{}, false, err
	}
	var (
		id, oldFeatures string
		oldPosition     int
	)
	err = tx.QueryRowContext(ctx, `SELECT id, features, position FROM project WHERE workspace = ? AND repo = ?`, workspace, repo).
		Scan(&id, &oldFeatures, &oldPosition)
	changed := true
	switch {
	case errors.Is(err, sql.ErrNoRows):
		id = newID("pj")
		stamp := now()
		_, err = tx.ExecContext(ctx, `INSERT INTO project (id, workspace, repo, position, features, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			id, workspace, repo, position, string(raw), stamp, stamp)
	case err != nil:
		return Project{}, false, err
	case oldFeatures == string(raw) && oldPosition == position:
		changed = false
	default:
		_, err = tx.ExecContext(ctx, `UPDATE project SET features = ?, position = ?, updated_at = ? WHERE id = ?`, string(raw), position, now(), id)
	}
	if err != nil {
		return Project{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return Project{}, false, err
	}
	p, err := s.projectByRepo(ctx, workspace, repo)
	return p, changed, err
}

func nonNilAny(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

// Projects lists a workspace's projects in file order, or only the named repositories.
func (s *Store) Projects(ctx context.Context, workspace string, repos ...string) ([]Project, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, workspace, repo, position, features, created_at, updated_at
		FROM project WHERE workspace = ? ORDER BY position, repo`, workspace)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Project
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		if len(repos) == 0 || slices.Contains(repos, p.Repo) {
			out = append(out, p)
		}
	}
	return out, rows.Err()
}

// projectByRepo returns one project, or ErrNotFound.
func (s *Store) projectByRepo(ctx context.Context, workspace, repo string) (Project, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, workspace, repo, position, features, created_at, updated_at
		FROM project WHERE workspace = ? AND repo = ?`, workspace, repo)
	p, err := scanProject(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Project{}, ErrNotFound
	}
	return p, err
}

type scanner interface{ Scan(...any) error }

func scanProject(r scanner) (Project, error) {
	var (
		p                     Project
		features, created, up string
	)
	if err := r.Scan(&p.ID, &p.Workspace, &p.Repo, &p.Position, &features, &created, &up); err != nil {
		return Project{}, err
	}
	if err := json.Unmarshal([]byte(features), &p.Features); err != nil {
		return Project{}, fmt.Errorf("store: project %s: stored features are not JSON: %w", p.Repo, err)
	}
	p.CreatedAt, p.UpdatedAt = parseTime(created), parseTime(up)
	return p, nil
}

// DeleteProject removes a project. It refuses, with ErrInUse, when work items name it:
// a project that carries history is not dropped by a file that stopped listing it.
func (s *Store) DeleteProject(ctx context.Context, id string) error {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM work_item WHERE project_id = ?`, id).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("%w: %d work items name this project", ErrInUse, n)
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM project WHERE id = ?`, id)
	return err
}

// WorkItem is the unit of work, independent of any tracker (ADR 0023).
type WorkItem struct {
	ID        string
	ProjectID string
	Title     string
	State     string
	// Payload is what the source last said about the item, as JSON (`{}` when it never
	// said anything). Its text is data from outside.
	Payload   json.RawMessage
	CreatedBy string
	CreatedAt time.Time
	UpdatedAt time.Time
	Refs      []Ref
}

// Ref ties a work item to something in a tracker, as tracker:key.
type Ref struct {
	Ref         string
	SourceEvent string
	AttachedBy  string
	AttachedAt  time.Time
}

// CreateWorkItem makes a work item and attaches its first references in one transaction.
// A reference that already belongs to a work item fails with ErrRefTaken and nothing is
// created.
func (s *Store) CreateWorkItem(ctx context.Context, projectID, title, actorID string, refs ...string) (WorkItem, error) {
	for _, r := range refs {
		if err := checkRef(r); err != nil {
			return WorkItem{}, err
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return WorkItem{}, err
	}
	defer tx.Rollback()
	id, stamp := newID("wi"), now()
	if _, err := tx.ExecContext(ctx, `INSERT INTO work_item (id, project_id, title, state, created_by, created_at, updated_at) VALUES (?, ?, ?, 'open', ?, ?, ?)`,
		id, projectID, title, actorID, stamp, stamp); err != nil {
		return WorkItem{}, err
	}
	for _, r := range refs {
		if err := attach(ctx, tx, id, r, "", actorID, stamp); err != nil {
			return WorkItem{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return WorkItem{}, err
	}
	return s.workItem(ctx, id)
}

// AttachRef adds a reference to a work item and returns the work item as it is now. One
// reference names one work item, so a reference held by another one fails with ErrRefTaken;
// moving it is an explicit operation, never a side effect.
func (s *Store) AttachRef(ctx context.Context, workItemID, ref, sourceEvent, actorID string) (WorkItem, error) {
	if err := checkRef(ref); err != nil {
		return WorkItem{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return WorkItem{}, err
	}
	defer tx.Rollback()
	if err := attach(ctx, tx, workItemID, ref, sourceEvent, actorID, now()); err != nil {
		return WorkItem{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE work_item SET updated_at = ? WHERE id = ?`, now(), workItemID); err != nil {
		return WorkItem{}, err
	}
	if err := tx.Commit(); err != nil {
		return WorkItem{}, err
	}
	return s.workItem(ctx, workItemID)
}

type execer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func attach(ctx context.Context, tx execer, workItemID, ref, sourceEvent, actorID, stamp string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO work_item_ref (work_item_id, ref, source_event, attached_by, attached_at) VALUES (?, ?, ?, ?, ?)`,
		workItemID, ref, sourceEvent, actorID, stamp)
	if isUnique(err) {
		return fmt.Errorf("%w: %s", ErrRefTaken, ref)
	}
	return err
}

func isUnique(err error) bool {
	var e *sqlite.Error
	// 1555 and 2067 are SQLITE_CONSTRAINT_PRIMARYKEY and SQLITE_CONSTRAINT_UNIQUE.
	return errors.As(err, &e) && (e.Code() == 1555 || e.Code() == 2067)
}

// WorkItemsByProject lists the work items of a project, newest first, with their references.
func (s *Store) WorkItemsByProject(ctx context.Context, projectID string) ([]WorkItem, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM work_item WHERE project_id = ? ORDER BY created_at DESC, id`, projectID)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	var out []WorkItem
	for _, id := range ids {
		w, err := s.workItem(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, nil
}

// workItem returns one work item with its references, or ErrNotFound.
func (s *Store) workItem(ctx context.Context, id string) (WorkItem, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, project_id, title, state, payload, created_by, created_at, updated_at FROM work_item WHERE id = ?`, id)
	return s.finishWorkItem(ctx, row)
}

// WorkItemByRef returns the work item that holds a reference, or ErrNotFound.
func (s *Store) WorkItemByRef(ctx context.Context, ref string) (WorkItem, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT w.id, w.project_id, w.title, w.state, w.payload, w.created_by, w.created_at, w.updated_at
		FROM work_item w JOIN work_item_ref r ON r.work_item_id = w.id WHERE r.ref = ?`, ref)
	return s.finishWorkItem(ctx, row)
}

func (s *Store) finishWorkItem(ctx context.Context, row *sql.Row) (WorkItem, error) {
	var w WorkItem
	var created, updated, payload string
	err := row.Scan(&w.ID, &w.ProjectID, &w.Title, &w.State, &payload, &w.CreatedBy, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return WorkItem{}, ErrNotFound
	}
	if err != nil {
		return WorkItem{}, err
	}
	w.Payload = json.RawMessage(payload)
	w.CreatedAt, w.UpdatedAt = parseTime(created), parseTime(updated)
	rows, err := s.db.QueryContext(ctx, `SELECT ref, source_event, attached_by, attached_at FROM work_item_ref WHERE work_item_id = ? ORDER BY attached_at, ref`, w.ID)
	if err != nil {
		return WorkItem{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var r Ref
		var at string
		if err := rows.Scan(&r.Ref, &r.SourceEvent, &r.AttachedBy, &at); err != nil {
			return WorkItem{}, err
		}
		r.AttachedAt = parseTime(at)
		w.Refs = append(w.Refs, r)
	}
	return w, rows.Err()
}
