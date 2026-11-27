package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// A worker token is how an unattended dots-worker (ADR 0042) keeps following the hub after the
// sign-in session of the person who set it up has ended. The owner issues it once, bound to a
// label, a login and a set of repositories; the worker trades it for hub tokens of 15 minutes
// (hubtoken.go), the way a refresh token is traded for access tokens. It lives for weeks, is
// revoked in one call, and is kept only as a SHA-256 hash in STATE_DIR/worker-tokens.json (mode
// 0600): the file lets nobody in, and the secret is shown once, at issue.

const (
	workerTokensFile = "worker-tokens.json"
	workerPrefix     = "dwt_"
	workerDefaultTTL = 30 * 24 * time.Hour
	workerMaxTTL     = 90 * 24 * time.Hour
)

type workerToken struct {
	ID       string    `json:"id"`
	Label    string    `json:"label"`
	Login    string    `json:"login"`
	Repos    []string  `json:"repos,omitempty"` // empty: every repository the dot knows
	Created  time.Time `json:"created"`
	Expires  time.Time `json:"expires"`
	LastUsed time.Time `json:"last_used,omitzero"`
	Revoked  bool      `json:"revoked,omitempty"`
}

type workerStore struct {
	mu        sync.Mutex
	path      string
	now       func() time.Time
	failDelay time.Duration // slows a wrong token down; a secret of 256 bits is not guessable anyway
	byHash    map[string]*workerToken
}

func newWorkerStore(stateDir string) *workerStore {
	s := &workerStore{now: time.Now, failDelay: 500 * time.Millisecond, byHash: map[string]*workerToken{}}
	if stateDir == "" {
		return s
	}
	s.path = filepath.Join(stateDir, workerTokensFile)
	info, err := os.Stat(s.path)
	if err != nil {
		return s
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&^0o600 != 0 {
		log.Printf("worker tokens: %s is not a private regular file, ignored", workerTokensFile)
		return s
	}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return s
	}
	var f struct {
		Tokens map[string]*workerToken `json:"tokens"`
	}
	if json.Unmarshal(raw, &f) != nil {
		log.Printf("worker tokens: %s is unreadable, starting with none", workerTokensFile)
		return s
	}
	if f.Tokens != nil {
		s.byHash = f.Tokens
	}
	return s
}

func hashWorkerSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// save writes the file atomically. The caller holds s.mu.
func (s *workerStore) save() error {
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

// issue creates a token and returns its secret, which nothing keeps.
func (s *workerStore) issue(label, login string, repos []string, ttl time.Duration) (string, workerToken, error) {
	label = strings.TrimSpace(label)
	if label == "" || len(label) > 60 {
		return "", workerToken{}, errors.New("a label of 1 to 60 characters is required")
	}
	if ttl <= 0 {
		ttl = workerDefaultTTL
	}
	if ttl > workerMaxTTL {
		return "", workerToken{}, errors.New("a worker token lives 90 days at most")
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", workerToken{}, err
	}
	secret := workerPrefix + base64.RawURLEncoding.EncodeToString(raw)
	hash := hashWorkerSecret(secret)
	now := s.now()
	t := &workerToken{ID: hash[:8], Label: label, Login: strings.TrimSpace(login), Repos: repos, Created: now, Expires: now.Add(ttl)}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byHash[hash] = t
	if err := s.save(); err != nil {
		delete(s.byHash, hash)
		return "", workerToken{}, err
	}
	return secret, *t, nil
}

// lookup finds a live token by its secret and notes the use.
func (s *workerStore) lookup(secret string) (workerToken, bool) {
	if !strings.HasPrefix(secret, workerPrefix) {
		return workerToken{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.byHash[hashWorkerSecret(secret)]
	if !ok || t.Revoked || !s.now().Before(t.Expires) {
		return workerToken{}, false
	}
	t.LastUsed = s.now()
	_ = s.save()
	return *t, true
}

func (s *workerStore) revoke(id string) bool {
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

func (s *workerStore) list() []workerToken {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]workerToken, 0, len(s.byHash))
	for _, t := range s.byHash {
		out = append(out, *t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out
}

// workerTokensAPI is the owner's side: GET lists, POST issues. The secret is in the answer of the
// POST and nowhere else.
func (d *dash) workerTokensAPI(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, map[string]any{"tokens": d.wtokens.list()})
	case http.MethodPost:
		var in struct {
			Label string   `json:"label"`
			Login string   `json:"login"`
			Repos []string `json:"repos"`
			Days  int      `json:"days"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
			return
		}
		known := map[string]bool{}
		for _, k := range d.knownRepos() {
			known[strings.ToLower(k)] = true
		}
		repos := make([]string, 0, len(in.Repos))
		for _, repo := range in.Repos {
			if repo = strings.ToLower(strings.TrimSpace(repo)); !known[repo] {
				writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "unknown repository " + repo})
				return
			}
			repos = append(repos, repo)
		}
		secret, t, err := d.wtokens.issue(in.Label, in.Login, repos, time.Duration(in.Days)*24*time.Hour)
		if err != nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, map[string]any{"token": secret, "id": t.ID, "expires_at": t.Expires.UTC().Format(time.RFC3339), "repos": t.Repos})
	default:
		http.Error(w, "GET or POST", http.StatusMethodNotAllowed)
	}
}

// workerTokenRevoke ends a token at once: the next exchange fails, and the hub drops the worker
// within the 15 minutes of the token it holds.
func (d *dash) workerTokenRevoke(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var in struct {
		ID string `json:"id"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&in) != nil || in.ID == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "an id is required"})
		return
	}
	if !d.wtokens.revoke(in.ID) {
		writeJSONStatus(w, http.StatusNotFound, map[string]string{"error": "no live token with that id"})
		return
	}
	writeJSON(w, map[string]string{"status": "revoked"})
}

// bearer is the token in an Authorization header, or "".
func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

// hubTokenForWorker trades a worker token for a hub token. It is the only thing the worker
// listener serves, so nothing else of the dashboard is reachable with it.
func (d *dash) hubTokenForWorker(w http.ResponseWriter, r *http.Request) {
	t, ok := d.wtokens.lookup(bearer(r))
	if !ok {
		time.Sleep(d.wtokens.failDelay)
		writeJSONStatus(w, http.StatusUnauthorized, map[string]string{"error": "invalid or revoked worker token"})
		return
	}
	repos := d.workerRepos(t)
	sub := t.Login
	if sub == "" {
		sub = "worker:" + t.Label
	}
	d.answerHubToken(w, sub, repos)
}

// workerRepos is the repositories a worker token may serve: the ones it lists, or every one the dot knows.
func (d *dash) workerRepos(t workerToken) []string {
	known := d.knownRepos()
	if len(t.Repos) == 0 {
		return known
	}
	allowed := map[string]bool{}
	for _, repo := range t.Repos {
		allowed[repo] = true
	}
	var repos []string
	for _, repo := range known {
		if allowed[strings.ToLower(repo)] {
			repos = append(repos, repo)
		}
	}
	return repos
}

// workerHandler is the mux of the worker listener: the exchange and nothing else.
func (d *dash) workerHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/hub-token", d.hubTokenForWorker)
	mux.HandleFunc("GET /api/hints", d.jobHints)
	mux.HandleFunc("GET /api/jobs", d.jobsPending)
	mux.HandleFunc("POST /api/jobs/{id}/claim", d.jobClaim)
	mux.HandleFunc("POST /api/jobs/{id}/heartbeat", d.jobHeartbeat)
	mux.HandleFunc("POST /api/jobs/{id}/finish", d.jobFinish)
	return mux
}
