package auth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	openRepo   = "acme/app"
	hiddenRepo = "acme/secret"
	tokenValue = "gho_visitor-token"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func logTo(t *testing.T) *bytes.Buffer {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	return &buf
}

// open builds an Auth that keeps its sessions in dir, on a clock the test moves.
func open(t *testing.T, dir string, c *clock) *Auth {
	t.Helper()
	return New(Config{ClientID: "x", StateDir: dir, Now: c.now})
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
	dir, clk := t.TempDir(), &clock{t: time.Now()}
	a := open(t, dir, clk)
	id := a.OpenSession("alice", map[string]bool{openRepo: true})
	a.flush()

	b := open(t, dir, clk) // a deploy over the same file
	if login, vis := b.SessionView(id); login != "alice" || !vis[openRepo] || vis[hiddenRepo] {
		t.Fatalf("the restored session is %q %v", login, vis)
	}
	clk.advance(TTL + time.Minute)
	if login, _ := b.SessionView(id); login != "" {
		t.Fatal("the restored session outlived its hour")
	}
}

func TestAnExpiredSessionDoesNotSurvive(t *testing.T) {
	dir, clk := t.TempDir(), &clock{t: time.Now()}
	a := open(t, dir, clk)
	id := a.OpenSession("alice", map[string]bool{openRepo: true})
	a.flush()
	clk.advance(TTL + time.Second)

	b := open(t, dir, clk)
	if b.SessionCount() != 0 {
		t.Fatal("an expired session was loaded")
	}
	if login, _ := b.SessionView(id); login != "" {
		t.Fatal("an expired session signed in")
	}
	b.flush()
	if s, _ := readStore(t, dir); len(s.Sessions) != 0 {
		t.Fatalf("the expired entry stayed in the file: %d", len(s.Sessions))
	}
}

func TestTheFileHoldsNoSecret(t *testing.T) {
	dir, clk := t.TempDir(), &clock{t: time.Now()}
	a := open(t, dir, clk)
	id := a.OpenSession("alice", map[string]bool{openRepo: true})
	a.flush()
	s, raw := readStore(t, dir)
	if len(s.Sessions) != 1 {
		t.Fatalf("%d entries", len(s.Sessions))
	}
	if bytes.Contains(raw, []byte(id)) {
		t.Fatal("the file holds the cookie id")
	}
	e, ok := s.Sessions[sessionKey(id)]
	if !ok || e.Login != "alice" || len(e.Visible) != 1 || e.Visible[0] != openRepo || !e.Expires.Equal(clk.now().Add(TTL)) || e.Created.IsZero() {
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
	dir := filepath.Join(t.TempDir(), "state")
	a := open(t, dir, &clock{t: time.Now()})
	a.OpenSession("alice", map[string]bool{openRepo: true})
	a.flush()
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
			a := New(Config{ClientID: "x", StateDir: dir})
			if a.SessionCount() != 0 {
				t.Fatalf("%d sessions loaded", a.SessionCount())
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
		if New(Config{ClientID: "x", StateDir: t.TempDir()}).SessionCount() != 0 {
			t.Fatal("sessions from nowhere")
		}
	})
}

func TestCloseSessionRemovesTheEntryFromTheFile(t *testing.T) {
	dir, clk := t.TempDir(), &clock{t: time.Now()}
	a := open(t, dir, clk)
	id := a.OpenSession("alice", map[string]bool{openRepo: true})
	a.flush()
	if s, _ := readStore(t, dir); len(s.Sessions) != 1 {
		t.Fatal("not saved")
	}
	a.CloseSession(id)
	a.flush()
	if s, _ := readStore(t, dir); len(s.Sessions) != 0 {
		t.Fatal("the entry stayed in the file after logout")
	}
	if login, _ := open(t, dir, clk).SessionView(id); login != "" {
		t.Fatal("a logged-out session came back")
	}
}

func TestTheDebouncedSaveWrites(t *testing.T) {
	dir := t.TempDir()
	a := open(t, dir, &clock{t: time.Now()})
	a.OpenSession("alice", map[string]bool{openRepo: true})
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
	dir, clk := t.TempDir(), &clock{t: time.Now()}
	a := open(t, dir, clk)
	first := ""
	for i := range maxSessions + 5 {
		id := a.OpenSession("bob", map[string]bool{openRepo: true})
		if i == 0 {
			first = id
		}
		clk.advance(time.Millisecond)
	}
	a.flush()
	if a.SessionCount() != maxSessions {
		t.Fatalf("%d sessions in memory", a.SessionCount())
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
	dir := t.TempDir()
	a := open(t, dir, &clock{t: time.Now()})
	var wg sync.WaitGroup
	for i := range 40 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := a.OpenSession(fmt.Sprintf("user%d", i), map[string]bool{openRepo: true})
			a.flush()
			if i%4 == 0 {
				a.CloseSession(id)
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
	blocker := filepath.Join(t.TempDir(), "file")
	os.WriteFile(blocker, []byte("x"), 0o600)
	clk := &clock{t: time.Now()}
	buf := logTo(t)
	a := open(t, filepath.Join(blocker, "state"), clk) // a directory cannot be made under a file
	id := a.OpenSession("alice", map[string]bool{openRepo: true})
	a.flush()
	a.flush()
	if login, _ := a.SessionView(id); login != "alice" {
		t.Fatal("sign-in broke on a failed write")
	}
	if n := strings.Count(buf.String(), "could not be written"); n != 1 {
		t.Fatalf("%d warnings in a minute, want 1", n)
	}
	clk.advance(2 * time.Minute)
	a.flush()
	if n := strings.Count(buf.String(), "could not be written"); n != 2 {
		t.Fatalf("%d warnings after a minute, want 2", n)
	}
}

func TestSignInOffCreatesNoFile(t *testing.T) {
	dir := t.TempDir()
	if New(Config{StateDir: dir}) != nil {
		t.Fatal("auth built without a client id")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("files created: %v", entries)
	}
}

func TestTheConfiguredHostsDefaultToGitHub(t *testing.T) {
	a := New(Config{ClientID: "x"})
	if a.githubURL != "https://github.com" || a.apiURL != "https://api.github.com" {
		t.Fatalf("defaults = %s %s", a.githubURL, a.apiURL)
	}
}
