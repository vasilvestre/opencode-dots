package store

import (
	"context"
	"errors"
	"strings"
	"time"
)

// Audit limits: a recorded field is a short name, never content.
const (
	DefaultAuditLimit = 100
	MaxAuditLimit     = 500
	maxAuditField     = 200
)

// AuditEntry is one write the dashboard served (ADR 0037). It holds the endpoint, the
// target (a file name or a record id), the caller and the HTTP status. It never holds a
// request body.
type AuditEntry struct {
	ID       int64
	At       time.Time
	Endpoint string
	Target   string
	Caller   string
	Outcome  int
}

// RecordAudit appends one entry stamped with the server's clock. The log has no update and
// no delete: the table refuses both.
func (s *Store) RecordAudit(ctx context.Context, endpoint, target, caller string, outcome int) error {
	if strings.TrimSpace(endpoint) == "" || strings.TrimSpace(caller) == "" {
		return errors.New("store: an audit entry names an endpoint and a caller")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO audit (at, endpoint, target, caller, outcome) VALUES (?, ?, ?, ?, ?)`,
		stamp(s.clock()), clip(endpoint), clip(target), clip(caller), outcome)
	return err
}

func clip(v string) string {
	v = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, v)
	if len(v) > maxAuditField {
		v = v[:maxAuditField]
	}
	return v
}

// RecentAudit lists the last entries, newest first. A limit outside 1..MaxAuditLimit
// takes the default or the maximum.
func (s *Store) RecentAudit(ctx context.Context, limit int) ([]AuditEntry, error) {
	switch {
	case limit <= 0:
		limit = DefaultAuditLimit
	case limit > MaxAuditLimit:
		limit = MaxAuditLimit
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, at, endpoint, target, caller, outcome FROM audit ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuditEntry{}
	for rows.Next() {
		var e AuditEntry
		var at string
		if err := rows.Scan(&e.ID, &at, &e.Endpoint, &e.Target, &e.Caller, &e.Outcome); err != nil {
			return nil, err
		}
		e.At = parseTime(at)
		out = append(out, e)
	}
	return out, rows.Err()
}
