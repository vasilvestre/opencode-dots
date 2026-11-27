package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// persistRig is a signed-in rig whose sessions are kept in a state directory of its own.
func persistRig(t *testing.T) (*rig, string) {
	t.Helper()
	r := newRig(t)
	dir := t.TempDir()
	r.d.auth.stateDir = dir
	return r, dir
}

// restart builds a second dashboard over the same file, as a deploy would.
func (r *rig) restart(dir string) *rig {
	r.t.Helper()
	r.d.auth.flush()
	a := testAuth(r.gh, "", r.clk)
	a.stateDir = dir
	a.loadSessions()
	d := newDash(r.d.state, r.d.root, r.targets, a)
	return &rig{t: r.t, gh: r.gh, clk: r.clk, d: d, h: routes(d, filepath.Join(r.d.root, "index.html"), nil), cookie: r.cookie, targets: r.targets}
}

func readStore(t *testing.T, dir string) (storedSessions, []byte) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, sessionsFile))
	if err != nil {
		t.Fatal(err)
	}
	var s storedSessions
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("the file is not JSON: %v", err)
	}
	return s, raw
}

func TestSessionSurvivesARestart(t *testing.T) {
	r, dir := persistRig(t)
	r.signIn()
	r2 := r.restart(dir)
	if r2.visitor("/api/repos").Code != 200 {
		t.Fatal("the session did not survive the restart")
	}
	if got := r2.d.auth.sessions[sessionKey(r.cookie)]; got.login != "alice" || !got.visible[openRepo] || got.visible[hiddenRepo] {
		t.Fatalf("the restored session is %+v", got)
	}
	r2.clk.advance(time.Hour + time.Minute)
	if r2.visitor("/api/repos").Code != 401 {
		t.Fatal("the restored session outlived its hour")
	}
}

func TestAnExpiredSessionDoesNotSurvive(t *testing.T) {
	r, dir := persistRig(t)
	r.signIn()
	r.d.auth.flush()
	r.clk.advance(time.Hour + time.Second)
	r2 := r.restart(dir)
	if len(r2.d.auth.sessions) != 0 {
		t.Fatal("an expired session was loaded")
	}
	if r2.visitor("/api/repos").Code != 401 {
		t.Fatal("an expired session signed in")
	}
	if s, _ := readStore(t, dir); len(s.Sessions) != 0 {
		t.Fatalf("the expired entry stayed in the file: %d", len(s.Sessions))
	}
}

func TestTheFileHoldsNoSecret(t *testing.T) {
	r, dir := persistRig(t)
	r.signIn()
	r.d.auth.flush()
	s, raw := readStore(t, dir)
	if len(s.Sessions) != 1 {
		t.Fatalf("%d entries", len(s.Sessions))
	}
	for _, secret := range []string{r.cookie, visitorToken, "devcode-secret", "ABCD-1234", "gho_"} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatalf("the file holds %q", secret)
		}
	}
	e, ok := s.Sessions[sessionKey(r.cookie)]
	if !ok || e.Login != "alice" || len(e.Visible) != 1 || e.Visible[0] != openRepo || !e.Expires.Equal(r.clk.now().Add(time.Hour)) || e.Created.IsZero() {
		t.Fatalf("entry = %+v", e)
	}
	var generic map[string]map[string]map[string]any
	json.Unmarshal(raw, &generic)
	for _, fields := range generic["sessions"] {
		if len(fields) != 4 {
			t.Fatalf("an entry has %d fields: %v", len(fields), fields)
		}
	}
	info, _ := os.Stat(filepath.Join(dir, sessionsFile))
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", info.Mode().Perm())
	}
}

func TestTheStateDirectoryIsCreatedPrivate(t *testing.T) {
	r := newRig(t)
	dir := filepath.Join(t.TempDir(), "state")
	r.d.auth.stateDir = dir
	r.signIn()
	r.d.auth.flush()
	info, err := os.Stat(dir)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("dir %v %v", info, err)
	}
}

func TestAnUnusableFileStartsEmpty(t *testing.T) {
	cases := map[string]struct {
		content string
		mode    os.FileMode
	}{
		"corrupted": {`{"version":1,"sessions":{`, 0o600},
		"empty":     {``, 0o600},
		"wide mode": {`{"version":1,"sessions":{"` + strings.Repeat("a", 64) + `":{"login":"alice","visible":["acme/app"],"expires":"2099-01-01T00:00:00Z","created":"2026-01-01T00:00:00Z"}}}`, 0o644},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, sessionsFile)
			os.WriteFile(path, []byte(c.content), c.mode)
			os.Chmod(path, c.mode)
			buf := logTo(t)
			a := newAuth(authConfig{ClientID: "x", StateDir: dir})
			if len(a.sessions) != 0 {
				t.Fatalf("%d sessions loaded", len(a.sessions))
			}
			if _, err := os.Stat(path); err != nil {
				t.Fatal("the file was deleted")
			}
			if strings.Contains(buf.String(), "alice") || strings.Contains(buf.String(), "acme") {
				t.Fatalf("the log holds session content: %s", buf)
			}
		})
	}
	t.Run("missing", func(t *testing.T) {
		a := newAuth(authConfig{ClientID: "x", StateDir: t.TempDir()})
		if len(a.sessions) != 0 {
			t.Fatal("sessions from nowhere")
		}
	})
}

func TestLogoutRemovesTheEntryFromTheFile(t *testing.T) {
	r, dir := persistRig(t)
	r.signIn()
	r.d.auth.flush()
	if s, _ := readStore(t, dir); len(s.Sessions) != 1 {
		t.Fatal("not saved")
	}
	ask(r.h, "POST", "/auth/logout", "visitor", r.cookie, "{}")
	r.d.auth.flush()
	if s, _ := readStore(t, dir); len(s.Sessions) != 0 {
		t.Fatal("the entry stayed in the file after logout")
	}
	if r.restart(dir).visitor("/api/repos").Code != 401 {
		t.Fatal("a logged-out session came back")
	}
}

func TestTheDebouncedSaveWrites(t *testing.T) {
	r, dir := persistRig(t)
	r.signIn()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(dir, sessionsFile)); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("no write after the debounce")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestTheStoreIsCapped(t *testing.T) {
	r, dir := persistRig(t)
	a := r.d.auth
	first := ""
	for i := 0; i < maxSessions+5; i++ {
		id := a.openSession("bob", map[string]bool{openRepo: true})
		if i == 0 {
			first = id
		}
		r.clk.advance(time.Millisecond)
	}
	a.flush()
	if len(a.sessions) != maxSessions {
		t.Fatalf("%d sessions in memory", len(a.sessions))
	}
	s, _ := readStore(t, dir)
	if len(s.Sessions) != maxSessions {
		t.Fatalf("%d sessions in the file", len(s.Sessions))
	}
	if _, ok := s.Sessions[sessionKey(first)]; ok {
		t.Fatal("the oldest session was kept")
	}
	a.mu.Lock()
	if a.saveTimer != nil {
		a.saveTimer.Stop()
	}
	a.mu.Unlock()
}

func TestConcurrentSignInsKeepTheFileValid(t *testing.T) {
	r, dir := persistRig(t)
	a := r.d.auth
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := a.openSession(fmt.Sprintf("user%d", i), map[string]bool{openRepo: true})
			a.flush()
			if i%4 == 0 {
				a.mu.Lock()
				delete(a.sessions, sessionKey(id))
				a.mu.Unlock()
			}
			a.flush()
		}(i)
	}
	wg.Wait()
	a.flush()
	s, _ := readStore(t, dir)
	if len(s.Sessions) != 30 {
		t.Fatalf("%d entries, want 30", len(s.Sessions))
	}
}

func TestAFailedWriteBreaksNothingAndWarnsOnce(t *testing.T) {
	r := newRig(t)
	blocker := filepath.Join(t.TempDir(), "file")
	os.WriteFile(blocker, []byte("x"), 0o600)
	r.d.auth.stateDir = filepath.Join(blocker, "state") // a directory cannot be made under a file
	buf := logTo(t)
	r.signIn()
	r.d.auth.flush()
	r.d.auth.flush()
	if r.visitor("/api/repos").Code != 200 {
		t.Fatal("sign-in broke on a failed write")
	}
	if n := strings.Count(buf.String(), "could not be written"); n != 1 {
		t.Fatalf("%d warnings in a minute, want 1", n)
	}
	r.clk.advance(2 * time.Minute)
	r.d.auth.flush()
	if n := strings.Count(buf.String(), "could not be written"); n != 2 {
		t.Fatalf("%d warnings after a minute, want 2", n)
	}
}

func TestSignInOffCreatesNoFile(t *testing.T) {
	dir := t.TempDir()
	if newAuth(authConfig{StateDir: dir}) != nil {
		t.Fatal("auth built without a client id")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("files created: %v", entries)
	}
}
