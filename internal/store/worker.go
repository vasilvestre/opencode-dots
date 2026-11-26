package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// A worker is a machine that offers its capacity to this instance (ADR 0042). The first step
// is presence only: it registers as a bot actor and keeps a heartbeat, the way a claim does.
// No task is sent to it.

// DefaultWorkerTTL and MaxWorkerTTL are the claim limits: a worker that stops heartbeating
// is listed as expired after 90 seconds, and no one holds a place for more than an hour.
const (
	DefaultWorkerTTL = DefaultClaimTTL
	MaxWorkerTTL     = MaxClaimTTL
)

// Worker is one registered machine.
type Worker struct {
	Name         string
	ActorID      string
	Machine      string
	Capabilities json.RawMessage // a JSON object
	RegisteredAt time.Time
	RenewedAt    time.Time
	ExpiresAt    time.Time
}

// Live reports whether the worker's time to live has not run out at the given time.
func (w Worker) Live(at time.Time) bool { return at.Before(w.ExpiresAt) }

func checkWorkerName(name string) error {
	if name == "" || strings.ContainsFunc(name, func(r rune) bool { return r <= ' ' || r == 0x7f }) {
		return fmt.Errorf("store: %q is not a worker name (no spaces, not empty)", name)
	}
	return nil
}

// RegisterWorker makes the worker, or renews it when the name exists: registering twice is
// one worker, and the registration time of a live one is kept. capabilities must be a JSON
// object, or empty. The expiry is the server's clock plus the time to live (0 takes the
// default, a larger one is capped).
func (s *Store) RegisterWorker(ctx context.Context, name, machine string, capabilities []byte, ttl time.Duration) (Worker, error) {
	if err := checkWorkerName(name); err != nil {
		return Worker{}, err
	}
	if strings.TrimSpace(machine) == "" {
		return Worker{}, errors.New("store: a worker names its machine")
	}
	caps := "{}"
	if len(strings.TrimSpace(string(capabilities))) > 0 {
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(capabilities, &obj); err != nil || obj == nil {
			return Worker{}, errors.New("store: capabilities must be a JSON object")
		}
		b, _ := json.Marshal(obj)
		caps = string(b)
	}
	who, err := s.EnsureActor(ctx, Actor{Kind: "bot", DisplayName: name, Identities: map[string]string{"worker": name}})
	if err != nil {
		return Worker{}, err
	}
	at := s.clock()
	expires := at.Add(clampTTL(ttl))
	_, err = s.db.ExecContext(ctx, `INSERT INTO worker (name, actor_id, machine, capabilities, registered_at, renewed_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET machine = excluded.machine, capabilities = excluded.capabilities,
			renewed_at = excluded.renewed_at, expires_at = excluded.expires_at,
			registered_at = CASE WHEN worker.expires_at > excluded.renewed_at THEN worker.registered_at ELSE excluded.registered_at END`,
		name, who.ID, machine, caps, stamp(at), stamp(at), stamp(expires))
	if err != nil {
		return Worker{}, err
	}
	return s.Worker(ctx, name)
}

// HeartbeatWorker extends a live worker by a time to live from now. One that expired or was
// expired by hand fails with ErrExpired and is not revived: it registers again.
func (s *Store) HeartbeatWorker(ctx context.Context, name string, ttl time.Duration) (Worker, error) {
	w, err := s.Worker(ctx, name)
	if err != nil {
		return Worker{}, err
	}
	at := s.clock()
	if !w.Live(at) {
		return w, ErrExpired
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE worker SET renewed_at = ?, expires_at = ? WHERE name = ?`,
		stamp(at), stamp(at.Add(clampTTL(ttl))), name); err != nil {
		return Worker{}, err
	}
	return s.Worker(ctx, name)
}

// ExpireWorker ends a worker now and returns it. Expiring one that is already over is not
// an error; an unknown name is ErrNotFound.
func (s *Store) ExpireWorker(ctx context.Context, name string) (Worker, error) {
	at := s.clock()
	if _, err := s.db.ExecContext(ctx, `UPDATE worker SET expires_at = ? WHERE name = ? AND expires_at > ?`, stamp(at), name, stamp(at)); err != nil {
		return Worker{}, err
	}
	return s.Worker(ctx, name)
}

// Worker looks one worker up by name.
func (s *Store) Worker(ctx context.Context, name string) (Worker, error) {
	ws, err := s.queryWorkers(ctx, `SELECT `+workerColumns+` FROM worker WHERE name = ?`, name)
	if err != nil {
		return Worker{}, err
	}
	if len(ws) == 0 {
		return Worker{}, ErrNotFound
	}
	return ws[0], nil
}

// Workers lists every worker, live ones first, then by name. Split live from expired with
// Worker.Live and Now.
func (s *Store) Workers(ctx context.Context) ([]Worker, error) {
	return s.queryWorkers(ctx, `SELECT `+workerColumns+` FROM worker ORDER BY (expires_at > ?) DESC, name`, stamp(s.clock()))
}

// Now is the time of this server, the clock every expiry is measured on.
func (s *Store) Now() time.Time { return s.clock() }

const workerColumns = `name, actor_id, machine, capabilities, registered_at, renewed_at, expires_at`

func (s *Store) queryWorkers(ctx context.Context, q string, args ...any) ([]Worker, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Worker{}
	for rows.Next() {
		var w Worker
		var caps, reg, ren, exp string
		if err := rows.Scan(&w.Name, &w.ActorID, &w.Machine, &caps, &reg, &ren, &exp); err != nil {
			return nil, err
		}
		w.Capabilities = json.RawMessage(caps)
		w.RegisteredAt, w.RenewedAt, w.ExpiresAt = parseTime(reg), parseTime(ren), parseTime(exp)
		out = append(out, w)
	}
	return out, rows.Err()
}
