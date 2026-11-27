// Package workertokens keeps the tokens of unattended dots-workers (ADR 0042). A worker token
// is how a dots-worker keeps following the hub after the sign-in session of the person who set it
// up has ended. The owner issues it once, bound to a label, a login and a set of repositories;
// the worker trades it for hub tokens of 15 minutes, the way a refresh token is traded for access
// tokens. It lives for weeks, is revoked in one call, and is kept only as a SHA-256 hash in
// STATE_DIR/worker-tokens.json (mode 0600): the file lets nobody in, and the secret is shown
// once, at issue.
package workertokens

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	File       = "worker-tokens.json"
	Prefix     = "dwt_"
	defaultTTL = 30 * 24 * time.Hour
	maxTTL     = 90 * 24 * time.Hour
)

type Token struct {
	ID       string    `json:"id"`
	Label    string    `json:"label"`
	Login    string    `json:"login"`
	Repos    []string  `json:"repos,omitempty"` // empty: every repository the dot knows
	Created  time.Time `json:"created"`
	Expires  time.Time `json:"expires"`
	LastUsed time.Time `json:"last_used,omitzero"`
	Revoked  bool      `json:"revoked,omitempty"`
}

type Store struct {
	mu   sync.Mutex
	path string
	// Now is the clock; FailDelay slows a wrong token down; a secret of 256 bits is not guessable anyway.
	Now       func() time.Time
	FailDelay time.Duration
	byHash    map[string]*Token
}

func New(stateDir string) *Store {
	s := &Store{Now: time.Now, FailDelay: 500 * time.Millisecond, byHash: map[string]*Token{}}
	if stateDir == "" {
		return s
	}
	s.path = filepath.Join(stateDir, File)
	info, err := os.Stat(s.path)
	if err != nil {
		return s
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&^0o600 != 0 {
		log.Printf("worker tokens: %s is not a private regular file, ignored", File)
		return s
	}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return s
	}
	var f struct {
		Tokens map[string]*Token `json:"tokens"`
	}
	if json.Unmarshal(raw, &f) != nil {
		log.Printf("worker tokens: %s is unreadable, starting with none", File)
		return s
	}
	if f.Tokens != nil {
		s.byHash = f.Tokens
	}
	return s
}

func hashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// save writes the file atomically. The caller holds s.mu.
func (s *Store) save() error {
	if s.path == "" {
		return nil
	}
	raw, err := json.MarshalIndent(map[string]any{"version": 1, "tokens": s.byHash}, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Issue creates a token and returns its secret, which nothing keeps.
func (s *Store) Issue(label, login string, repos []string, ttl time.Duration) (string, Token, error) {
	label = strings.TrimSpace(label)
	if label == "" || len(label) > 60 {
		return "", Token{}, errors.New("a label of 1 to 60 characters is required")
	}
	if ttl <= 0 {
		ttl = defaultTTL
	}
	if ttl > maxTTL {
		return "", Token{}, errors.New("a worker token lives 90 days at most")
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", Token{}, err
	}
	secret := Prefix + base64.RawURLEncoding.EncodeToString(raw)
	hash := hashSecret(secret)
	now := s.Now()
	t := &Token{ID: hash[:8], Label: label, Login: strings.TrimSpace(login), Repos: repos, Created: now, Expires: now.Add(ttl)}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byHash[hash] = t
	if err := s.save(); err != nil {
		delete(s.byHash, hash)
		return "", Token{}, err
	}
	return secret, *t, nil
}

// Lookup finds a live token by its secret and notes the use.
func (s *Store) Lookup(secret string) (Token, bool) {
	if !strings.HasPrefix(secret, Prefix) {
		return Token{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.byHash[hashSecret(secret)]
	if !ok || t.Revoked || !s.Now().Before(t.Expires) {
		return Token{}, false
	}
	t.LastUsed = s.Now()
	_ = s.save()
	return *t, true
}

func (s *Store) Revoke(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.byHash {
		if t.ID == id && !t.Revoked {
			t.Revoked = true
			_ = s.save()
			return true
		}
	}
	return false
}

func (s *Store) List() []Token {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Token, 0, len(s.byHash))
	for _, t := range s.byHash {
		out = append(out, *t)
	}
	slices.SortFunc(out, func(a, b Token) int { return b.Created.Compare(a.Created) })
	return out
}

// Allowed is the part of known (repository names as the dot spells them) this token may serve:
// the ones it lists, or all of them when it lists none.
func (t Token) Allowed(known []string) []string {
	if len(t.Repos) == 0 {
		return known
	}
	var repos []string
	for _, repo := range known {
		if slices.Contains(t.Repos, strings.ToLower(repo)) {
			repos = append(repos, repo)
		}
	}
	return repos
}

// ExpiresAt is the expiry as the API shows it.
func (t Token) ExpiresAt() string { return t.Expires.UTC().Format(time.RFC3339) }
