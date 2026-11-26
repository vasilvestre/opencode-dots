package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrExpired is returned when a claim is renewed after it expired or was released. The
// holder must claim again, knowing the bot may have worked in between.
var ErrExpired = errors.New("store: the claim is no longer active")

const (
	// DefaultClaimTTL is how long a claim lives without a heartbeat.
	DefaultClaimTTL = 90 * time.Second
	// MaxClaimTTL bounds what a client may ask for, so a forgotten claim cannot hold a
	// work item for long.
	MaxClaimTTL = time.Hour
)

// Claim says a human works on a work item (ADR 0026). It expires by itself when the
// heartbeats stop.
type Claim struct {
	ID         string
	WorkItemID string
	ActorID    string
	ActorKind  string
	Machine    string
	Branch     string
	CreatedAt  time.Time
	RenewedAt  time.Time
	ExpiresAt  time.Time
	ReleasedAt time.Time // zero while it is held
}

// Expires is the expiry as an RFC 3339 time, for a person or a log to read.
func (c Claim) Expires() string { return c.ExpiresAt.UTC().Format(time.RFC3339) }

// HeldSince is the creation as an RFC 3339 time: the age the dashboard shows.
func (c Claim) HeldSince() string { return c.CreatedAt.UTC().Format(time.RFC3339) }

// Renewed reports whether the claim has been renewed since it was made.
func (c Claim) Renewed() bool { return !c.RenewedAt.Equal(c.CreatedAt) }

// Active reports whether the claim holds at the given time.
func (c Claim) Active(at time.Time) bool { return c.ReleasedAt.IsZero() && at.Before(c.ExpiresAt) }

func clampTTL(ttl time.Duration) time.Duration {
	switch {
	case ttl <= 0:
		return DefaultClaimTTL
	case ttl > MaxClaimTTL:
		return MaxClaimTTL
	}
	return ttl
}

// TakeClaim makes the actor's claim on a work item from a machine, or renews the one it
// already holds from that machine: asking twice is one claim. The expiry is the server's
// clock plus the time to live, which is capped at MaxClaimTTL. A second human claim from
// another machine is allowed (ADR 0026 warns both holders, it never blocks): see
// ActiveClaims.
func (s *Store) TakeClaim(ctx context.Context, workItemID, actorID, machine, branch string, ttl time.Duration) (Claim, error) {
	if strings.TrimSpace(machine) == "" {
		return Claim{}, errors.New("store: a claim names the machine that holds it")
	}
	at := s.clock()
	expires := at.Add(clampTTL(ttl))
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Claim{}, err
	}
	defer tx.Rollback()
	var id string
	err = tx.QueryRowContext(ctx, `SELECT id FROM claim WHERE work_item_id = ? AND machine = ? AND released_at = ''`, workItemID, machine).Scan(&id)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		id = newID("cl")
		_, err = tx.ExecContext(ctx, `INSERT INTO claim (id, work_item_id, actor_id, machine, branch, created_at, renewed_at, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			id, workItemID, actorID, machine, branch, stamp(at), stamp(at), stamp(expires))
	case err == nil:
		_, err = tx.ExecContext(ctx, `UPDATE claim SET actor_id = ?, branch = ?, renewed_at = ?, expires_at = ? WHERE id = ?`, actorID, branch, stamp(at), stamp(expires), id)
	}
	if err != nil {
		return Claim{}, err
	}
	if err := tx.Commit(); err != nil {
		return Claim{}, err
	}
	return s.claim(ctx, id)
}

// Heartbeat extends a claim by a time to live from now. A claim that expired or was
// released fails with ErrExpired and is not revived.
func (s *Store) Heartbeat(ctx context.Context, claimID string, ttl time.Duration) (Claim, error) {
	c, err := s.claim(ctx, claimID)
	if err != nil {
		return Claim{}, err
	}
	at := s.clock()
	if !c.Active(at) {
		return c, ErrExpired
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE claim SET renewed_at = ?, expires_at = ? WHERE id = ? AND released_at = ''`, stamp(at), stamp(at.Add(clampTTL(ttl))), claimID); err != nil {
		return Claim{}, err
	}
	return s.claim(ctx, claimID)
}

// ReleaseClaim ends a claim now and returns it. Releasing one that is already over is not an
// error.
func (s *Store) ReleaseClaim(ctx context.Context, claimID string) (Claim, error) {
	if _, err := s.db.ExecContext(ctx, `UPDATE claim SET released_at = ? WHERE id = ? AND released_at = ''`, stamp(s.clock()), claimID); err != nil {
		return Claim{}, err
	}
	return s.claim(ctx, claimID)
}

const claimColumns = `c.id, c.work_item_id, c.actor_id, a.kind, c.machine, c.branch, c.created_at, c.renewed_at, c.expires_at, c.released_at`

// ActiveClaims lists the claims that hold on a work item now. Two or more held by humans is
// the case ADR 0026 warns about.
func (s *Store) ActiveClaims(ctx context.Context, workItemID string) ([]Claim, error) {
	return s.queryClaims(ctx, `SELECT `+claimColumns+` FROM claim c JOIN actor a ON a.id = c.actor_id
		WHERE c.work_item_id = ? AND c.released_at = '' AND c.expires_at > ? ORDER BY c.created_at`, workItemID, stamp(s.clock()))
}

// LiveClaim is an active claim with the repository and title of its work item, for the
// dashboard. It is read-only.
type LiveClaim struct {
	Claim
	Repo  string
	Title string
}

// LiveClaims lists every claim that still holds, with its repository, oldest first.
func (s *Store) LiveClaims(ctx context.Context) ([]LiveClaim, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+claimColumns+`,
		(SELECT p.repo FROM work_item w JOIN project p ON p.id = w.project_id WHERE w.id = c.work_item_id),
		(SELECT w.title FROM work_item w WHERE w.id = c.work_item_id)
		FROM claim c JOIN actor a ON a.id = c.actor_id
		WHERE c.released_at = '' AND c.expires_at > ? ORDER BY c.created_at`, stamp(s.clock()))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LiveClaim
	for rows.Next() {
		var l LiveClaim
		var created, renewed, expires, released string
		if err := rows.Scan(&l.ID, &l.WorkItemID, &l.ActorID, &l.ActorKind, &l.Machine, &l.Branch,
			&created, &renewed, &expires, &released, &l.Repo, &l.Title); err != nil {
			return nil, err
		}
		l.CreatedAt, l.RenewedAt, l.ExpiresAt = parseTime(created), parseTime(renewed), parseTime(expires)
		if released != "" {
			l.ReleasedAt = parseTime(released)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// HumanClaimOnBranch reports whether a human holds an active claim that covers a branch of a
// repository: a claim naming the branch, or one on a work item that holds the reference
// branch:repo:name. It is what a shell step asks before it pushes.
func (s *Store) HumanClaimOnBranch(ctx context.Context, repo, branch string) (Claim, bool, error) {
	claims, err := s.queryClaims(ctx, `SELECT `+claimColumns+` FROM claim c JOIN actor a ON a.id = c.actor_id
		WHERE c.released_at = '' AND c.expires_at > ? AND a.kind = 'human'
		  AND ((c.branch = ? AND c.work_item_id IN (SELECT w.id FROM work_item w JOIN project p ON p.id = w.project_id WHERE p.repo = ?))
		       OR c.work_item_id IN (SELECT work_item_id FROM work_item_ref WHERE ref = ?))
		ORDER BY c.created_at`, stamp(s.clock()), branch, repo, fmt.Sprintf("branch:%s:%s", repo, branch))
	if err != nil || len(claims) == 0 {
		return Claim{}, false, err
	}
	return claims[0], true, nil
}

func (s *Store) claim(ctx context.Context, id string) (Claim, error) {
	claims, err := s.queryClaims(ctx, `SELECT `+claimColumns+` FROM claim c JOIN actor a ON a.id = c.actor_id WHERE c.id = ?`, id)
	if err != nil {
		return Claim{}, err
	}
	if len(claims) == 0 {
		return Claim{}, ErrNotFound
	}
	return claims[0], nil
}

func (s *Store) queryClaims(ctx context.Context, query string, args ...any) ([]Claim, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Claim
	for rows.Next() {
		var c Claim
		var created, renewed, expires, released string
		if err := rows.Scan(&c.ID, &c.WorkItemID, &c.ActorID, &c.ActorKind, &c.Machine, &c.Branch, &created, &renewed, &expires, &released); err != nil {
			return nil, err
		}
		c.CreatedAt, c.RenewedAt, c.ExpiresAt = parseTime(created), parseTime(renewed), parseTime(expires)
		if released != "" {
			c.ReleasedAt = parseTime(released)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
