// Package store is the control-plane database of ADR 0024: projects, work items and
// the references that tie a work item to a tracker (ADR 0023). SQLite in WAL mode, one
// file, one writer. The rest of the code talks to *Store, so a move to another engine
// changes this package only.
package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"

	_ "modernc.org/sqlite" // registers the "sqlite" driver, pure Go
)

var (
	// ErrNotFound is returned when a lookup matches nothing.
	ErrNotFound = errors.New("store: not found")
	// ErrRefTaken is returned when a reference already belongs to a work item: one
	// reference names one work item (ADR 0023).
	ErrRefTaken = errors.New("store: reference already attached to a work item")
	// ErrInUse is returned when a project cannot be deleted because work items name it.
	ErrInUse = errors.New("store: still in use")
)

// Store is an open database.
type Store struct {
	db *sql.DB
	// clock is the time of this server. Claims never take a time from a client; tests set it.
	clock func() time.Time
}

// Open opens the database at path, creating the file and its directory, and brings the
// schema up to date. The file holds private names, so it is created with mode 0600.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600); err != nil {
		return nil, err
	} else if err := f.Close(); err != nil {
		return nil, err
	}
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One writer: a single connection keeps SQLite from answering "database is locked".
	db.SetMaxOpenConns(1)
	s := &Store{db: db, clock: time.Now}
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// migrations are applied in order, each in its own transaction, and never edited once
// shipped: a change is a new entry.
var migrations = []string{
	`CREATE TABLE workspace (
		name     TEXT PRIMARY KEY,
		settings TEXT NOT NULL DEFAULT '{}'
	);
	CREATE TABLE actor (
		id           TEXT PRIMARY KEY,
		kind         TEXT NOT NULL CHECK (kind IN ('human', 'bot')),
		display_name TEXT NOT NULL,
		identities   TEXT NOT NULL DEFAULT '{}',
		created_at   TEXT NOT NULL
	);
	CREATE TABLE project (
		id         TEXT PRIMARY KEY,
		workspace  TEXT NOT NULL REFERENCES workspace(name),
		repo       TEXT NOT NULL,
		position   INTEGER NOT NULL DEFAULT 0,
		features   TEXT NOT NULL DEFAULT '{}',
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		UNIQUE (workspace, repo)
	);
	CREATE TABLE work_item (
		id         TEXT PRIMARY KEY,
		project_id TEXT NOT NULL REFERENCES project(id) ON DELETE RESTRICT,
		title      TEXT NOT NULL DEFAULT '',
		state      TEXT NOT NULL DEFAULT 'open',
		created_by TEXT NOT NULL REFERENCES actor(id),
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	);
	CREATE TABLE work_item_ref (
		work_item_id TEXT NOT NULL REFERENCES work_item(id) ON DELETE CASCADE,
		ref          TEXT NOT NULL UNIQUE,
		source_event TEXT NOT NULL DEFAULT '',
		attached_by  TEXT NOT NULL REFERENCES actor(id),
		attached_at  TEXT NOT NULL
	);
	CREATE INDEX work_item_project ON work_item(project_id);`,
	// 2: claims (ADR 0026). Every time is the server's own clock.
	`CREATE TABLE claim (
		id           TEXT PRIMARY KEY,
		work_item_id TEXT NOT NULL REFERENCES work_item(id) ON DELETE CASCADE,
		actor_id     TEXT NOT NULL REFERENCES actor(id),
		machine      TEXT NOT NULL,
		branch       TEXT NOT NULL DEFAULT '',
		created_at   TEXT NOT NULL,
		renewed_at   TEXT NOT NULL,
		expires_at   TEXT NOT NULL,
		released_at  TEXT NOT NULL DEFAULT ''
	);
	CREATE UNIQUE INDEX claim_one_live_per_machine ON claim(work_item_id, machine) WHERE released_at = '';
	CREATE INDEX claim_work_item ON claim(work_item_id);`,
	// 3: the audit log of dashboard writes (ADR 0037). Append only: the triggers refuse an
	// update or a delete, so a bug cannot rewrite history.
	`CREATE TABLE audit (
		id       INTEGER PRIMARY KEY AUTOINCREMENT,
		at       TEXT NOT NULL,
		endpoint TEXT NOT NULL,
		target   TEXT NOT NULL DEFAULT '',
		caller   TEXT NOT NULL,
		outcome  INTEGER NOT NULL
	);
	CREATE TRIGGER audit_no_update BEFORE UPDATE ON audit BEGIN SELECT RAISE(ABORT, 'audit is append only'); END;
	CREATE TRIGGER audit_no_delete BEFORE DELETE ON audit BEGIN SELECT RAISE(ABORT, 'audit is append only'); END;`,
	// 4: workers (ADR 0042). Presence only: a bot actor that heartbeats, like a claim.
	`CREATE TABLE worker (
		name          TEXT PRIMARY KEY,
		actor_id      TEXT NOT NULL REFERENCES actor(id),
		machine       TEXT NOT NULL,
		capabilities  TEXT NOT NULL DEFAULT '{}',
		registered_at TEXT NOT NULL,
		renewed_at    TEXT NOT NULL,
		expires_at    TEXT NOT NULL
	);`,
	// 5: what a source says about a work item (ADR 0039): the latest payload, and an
	// append-only trail of the changes. Both are data from outside, never instructions.
	`ALTER TABLE work_item ADD COLUMN payload TEXT NOT NULL DEFAULT '{}';
	CREATE TABLE work_item_event (
		id           INTEGER PRIMARY KEY AUTOINCREMENT,
		work_item_id TEXT NOT NULL REFERENCES work_item(id) ON DELETE CASCADE,
		at           TEXT NOT NULL,
		kind         TEXT NOT NULL,
		actor_id     TEXT NOT NULL REFERENCES actor(id),
		data         TEXT NOT NULL DEFAULT '{}'
	);
	CREATE INDEX work_item_event_item ON work_item_event(work_item_id, id);`,
	// 6: link proposals (ADR 0041, first step). A proposal is data and changes nothing by
	// itself; accepting one moves references (link.go). A pair is unique, in either order,
	// while it is open. merged_into points a merged work item at the one that took its
	// references; its history is in work_item_event.
	`ALTER TABLE work_item ADD COLUMN merged_into TEXT NOT NULL DEFAULT '';
	CREATE TABLE link_proposal (
		id           TEXT PRIMARY KEY,
		work_item_a  TEXT NOT NULL REFERENCES work_item(id) ON DELETE CASCADE,
		work_item_b  TEXT NOT NULL REFERENCES work_item(id) ON DELETE CASCADE,
		confidence   REAL NOT NULL CHECK (confidence >= 0 AND confidence <= 1),
		reason       TEXT NOT NULL DEFAULT '',
		evidence     TEXT NOT NULL DEFAULT '{}',
		state        TEXT NOT NULL DEFAULT 'open' CHECK (state IN ('open', 'accepted', 'declined')),
		proposed_by  TEXT NOT NULL,
		created_at   TEXT NOT NULL,
		decided_by   TEXT NOT NULL DEFAULT '',
		decided_at   TEXT NOT NULL DEFAULT '',
		CHECK (work_item_a <> work_item_b)
	);
	CREATE UNIQUE INDEX link_proposal_open_pair ON link_proposal(min(work_item_a, work_item_b), max(work_item_a, work_item_b)) WHERE state = 'open';`,

	// 7: a split link proposal. The state CHECK of migration 6 cannot take a new value, so
	// the table is rebuilt (nothing references it): the rows are copied as they are, and
	// split_by and split_at say who undid an accepted merge and when. The open-pair index
	// is created again.
	`CREATE TABLE link_proposal_new (
		id           TEXT PRIMARY KEY,
		work_item_a  TEXT NOT NULL REFERENCES work_item(id) ON DELETE CASCADE,
		work_item_b  TEXT NOT NULL REFERENCES work_item(id) ON DELETE CASCADE,
		confidence   REAL NOT NULL CHECK (confidence >= 0 AND confidence <= 1),
		reason       TEXT NOT NULL DEFAULT '',
		evidence     TEXT NOT NULL DEFAULT '{}',
		state        TEXT NOT NULL DEFAULT 'open' CHECK (state IN ('open', 'accepted', 'declined', 'split')),
		proposed_by  TEXT NOT NULL,
		created_at   TEXT NOT NULL,
		decided_by   TEXT NOT NULL DEFAULT '',
		decided_at   TEXT NOT NULL DEFAULT '',
		split_by     TEXT NOT NULL DEFAULT '',
		split_at     TEXT NOT NULL DEFAULT '',
		CHECK (work_item_a <> work_item_b)
	);
	INSERT INTO link_proposal_new (id, work_item_a, work_item_b, confidence, reason, evidence, state, proposed_by, created_at, decided_by, decided_at)
		SELECT id, work_item_a, work_item_b, confidence, reason, evidence, state, proposed_by, created_at, decided_by, decided_at FROM link_proposal;
	DROP TABLE link_proposal;
	ALTER TABLE link_proposal_new RENAME TO link_proposal;
	CREATE UNIQUE INDEX link_proposal_open_pair ON link_proposal(min(work_item_a, work_item_b), max(work_item_a, work_item_b)) WHERE state = 'open';`,
}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`); err != nil {
		return err
	}
	var current int
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_version`).Scan(&current)
	if err != nil {
		return err
	}
	if current > len(migrations) {
		return fmt.Errorf("store: the database is at schema %d, newer than this build (%d)", current, len(migrations))
	}
	for i := current; i < len(migrations); i++ {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("store: migration %d: %w", i+1, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_version (version) VALUES (?)`, i+1); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// schemaVersion returns the version the database is at.
func (s *Store) schemaVersion(ctx context.Context) (int, error) {
	var v int
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_version`).Scan(&v)
	return v, err
}

// newID makes an opaque identifier: a short prefix naming the kind, then 128 random bits.
func newID(prefix string) string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err) // the system's random source failing is not recoverable
	}
	return prefix + "_" + hex.EncodeToString(b)
}

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}

var refPattern = regexp.MustCompile(`^[a-z][a-z0-9-]*:\S+$`)

// checkRef validates a reference of the form tracker:key, such as github:owner/repo#12,
// branch:owner/repo:name or jira:ABC-12. It does not look the tracker up.
func checkRef(ref string) error {
	if !refPattern.MatchString(ref) || strings.ContainsFunc(ref, unicode.IsControl) {
		return fmt.Errorf("store: %q is not a reference (want tracker:key, no spaces)", ref)
	}
	return nil
}
