package main

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// The sign-in sessions survive a restart (docs/dashboard-auth.md). They are kept in
// STATE_DIR/sessions.json, keyed by the SHA-256 of the cookie id: the file lets nobody sign in,
// because the cookie id itself is never written. An entry holds the login, the visible
// repositories, the expiry and the creation time. There is no token to write, and the device
// code and the user code never reach a session.

const (
	sessionsFile    = "sessions.json"
	sessionsVersion = 1
	saveDebounce    = time.Second
	warnEvery       = time.Minute
)

type storedSession struct {
	Login   string    `json:"login"`
	Visible []string  `json:"visible"`
	Expires time.Time `json:"expires"`
	Created time.Time `json:"created"`
}

type storedSessions struct {
	Version  int                      `json:"version"`
	Sessions map[string]storedSession `json:"sessions"`
}

// sessionKey is what the store and the file know a session by: the hash of the cookie id.
func sessionKey(id string) string {
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:])
}

// findSession looks a cookie id up by its hash, then compares the hashes in constant time. The
// caller holds a.mu.
func (a *authState) findSession(id string) (string, session, bool) {
	key := sessionKey(id)
	s, ok := a.sessions[key]
	if !ok || subtle.ConstantTimeCompare([]byte(key), []byte(sessionKey(id))) != 1 {
		return "", session{}, false
	}
	return key, s, true
}

func (a *authState) storePath() string {
	if a.stateDir == "" {
		return ""
	}
	return filepath.Join(a.stateDir, sessionsFile)
}

// loadSessions reads the file once at start. A missing, empty, unreadable or corrupted file, or one
// readable by others, starts empty and is left in place. Nothing of its content is logged.
func (a *authState) loadSessions() {
	path := a.storePath()
	if path == "" {
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("sign-in: the session file cannot be read, starting with none")
		}
		return
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&^0o600 != 0 {
		log.Printf("sign-in: the session file is not a private regular file (mode %v), ignored", info.Mode().Perm())
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) == 0 {
		return
	}
	var in storedSessions
	if json.Unmarshal(raw, &in) != nil || in.Version != sessionsVersion {
		log.Printf("sign-in: the session file is corrupted or unknown, ignored")
		return
	}
	now := a.now()
	for key, s := range in.Sessions {
		if len(key) != 64 || !loginRE.MatchString(s.Login) || !now.Before(s.Expires) {
			continue
		}
		vis := make(map[string]bool, len(s.Visible))
		for _, r := range s.Visible {
			vis[r] = true
		}
		a.sessions[key] = session{login: s.Login, visible: vis, expires: s.Expires, created: s.Created}
	}
	a.capSessions()
}

// capSessions drops the oldest sessions beyond maxSessions. The caller holds a.mu or is single
// threaded.
func (a *authState) capSessions() {
	for len(a.sessions) > maxSessions {
		oldest, at := "", time.Time{}
		for k, s := range a.sessions {
			if oldest == "" || s.created.Before(at) {
				oldest, at = k, s.created
			}
		}
		delete(a.sessions, oldest)
	}
}

// pruneSessions deletes the expired ones and says whether any went. The caller holds a.mu.
func (a *authState) pruneSessions() bool {
	now, pruned := a.now(), false
	for k, s := range a.sessions {
		if !now.Before(s.expires) {
			delete(a.sessions, k)
			pruned = true
		}
	}
	return pruned
}

// scheduleSave asks for a write soon; a burst of changes makes one write. The caller holds a.mu.
func (a *authState) scheduleSave() {
	if a.storePath() == "" || a.saveTimer != nil {
		return
	}
	a.saveTimer = time.AfterFunc(saveDebounce, func() {
		a.mu.Lock()
		a.saveTimer = nil
		a.mu.Unlock()
		a.flush()
	})
}

// flush prunes, then writes the file now. A failure is logged at most once a minute and never
// reaches the visitor: the sessions stay in memory.
func (a *authState) flush() {
	path := a.storePath()
	if path == "" {
		return
	}
	a.fileMu.Lock()
	defer a.fileMu.Unlock()
	a.mu.Lock()
	a.pruneSessions()
	out := storedSessions{Version: sessionsVersion, Sessions: make(map[string]storedSession, len(a.sessions))}
	for k, s := range a.sessions {
		vis := make([]string, 0, len(s.visible))
		for r, ok := range s.visible {
			if ok {
				vis = append(vis, r)
			}
		}
		sort.Strings(vis)
		out.Sessions[k] = storedSession{Login: s.login, Visible: vis, Expires: s.expires, Created: s.created}
	}
	a.mu.Unlock()
	if err := writeSessionsFile(path, out); err != nil {
		a.mu.Lock()
		now := a.now()
		warn := a.lastWarn.IsZero() || now.Sub(a.lastWarn) >= warnEvery
		if warn {
			a.lastWarn = now
		}
		a.mu.Unlock()
		if warn {
			log.Printf("sign-in: the session file could not be written (%v), sessions stay in memory", err)
		}
	}
}

// sweepSessions prunes the expired sessions every few minutes and saves when any went, so the
// file does not keep a stale entry until the next sign-in.
func (a *authState) sweepSessions(every time.Duration) {
	for range time.Tick(every) {
		a.mu.Lock()
		if a.pruneSessions() {
			a.scheduleSave()
		}
		a.mu.Unlock()
	}
}

// writeSessionsFile writes atomically: a 0600 temp file in the same directory, fsync, rename.
func writeSessionsFile(path string, in storedSessions) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(in)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".sessions-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	ok := false
	defer func() {
		if !ok {
			f.Close()
			os.Remove(tmp)
		}
	}()
	if err := f.Chmod(0o600); err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	ok = true
	return nil
}
