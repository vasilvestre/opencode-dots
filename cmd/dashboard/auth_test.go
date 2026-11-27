package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"opencode-dots/internal/auth"
	"opencode-dots/internal/jobs"
	"opencode-dots/internal/runrecorder"
	"opencode-dots/internal/workertokens"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"opencode-dots/internal/store"
)

const (
	visitorToken = "gho_visitor_secret_0002"
	openRepo     = "acme/app"
	hiddenRepo   = "acme/client"
)

// fakeGitHub answers what the dashboard asks of GitHub: the device flow, GET /user, the GraphQL
// viewerPermission and the REST repository. Nothing here reaches the real GitHub.
type fakeGitHub struct {
	*httptest.Server
	mu         sync.Mutex
	scopes     []string
	tokenReply []map[string]any // consumed one per poll; then authorization_pending
	tokenCalls int
	userCalls  int
	gqlCalls   int
	gqlQueries []string
	restCalls  int
	perms      map[string]string // "owner/repo" -> alice's permission: none, read, triage, write, maintain, admin
	gqlStatus  int               // forced status for GraphQL, 0 for none
	restStatus int               // forced status for the REST repository, 0 for none
	expiresIn  int
	interval   int
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	t.Helper()
	g := &fakeGitHub{perms: map[string]string{}, expiresIn: 900, interval: 5}
	mux := http.NewServeMux()
	mux.HandleFunc("/login/device/code", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		g.mu.Lock()
		defer g.mu.Unlock()
		if _, ok := r.PostForm["scope"]; !ok || r.PostForm.Get("client_id") != "client-1" {
			http.Error(w, "bad", http.StatusBadRequest)
			return
		}
		g.scopes = append(g.scopes, r.PostForm.Get("scope"))
		json.NewEncoder(w).Encode(map[string]any{"device_code": "devcode-secret", "user_code": "ABCD-1234",
			"verification_uri": g.URL + "/login/device", "expires_in": g.expiresIn, "interval": g.interval})
	})
	mux.HandleFunc("/login/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		g.mu.Lock()
		defer g.mu.Unlock()
		g.tokenCalls++
		if r.PostForm.Get("device_code") != "devcode-secret" || r.PostForm.Get("grant_type") != "urn:ietf:params:oauth:grant-type:device_code" {
			http.Error(w, "bad", http.StatusBadRequest)
			return
		}
		reply := map[string]any{"error": "authorization_pending"}
		if len(g.tokenReply) > 0 {
			reply, g.tokenReply = g.tokenReply[0], g.tokenReply[1:]
		}
		json.NewEncoder(w).Encode(reply)
	})
	mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		defer g.mu.Unlock()
		g.userCalls++
		if r.Header.Get("Authorization") != "Bearer "+visitorToken {
			http.Error(w, "bad", http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"login": "alice"})
	})
	mux.HandleFunc("/graphql", func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		defer g.mu.Unlock()
		g.gqlCalls++
		if r.Header.Get("Authorization") != "Bearer "+visitorToken {
			http.Error(w, "bad", http.StatusUnauthorized)
			return
		}
		if g.gqlStatus != 0 {
			http.Error(w, "forced", g.gqlStatus)
			return
		}
		var in struct {
			Query string `json:"query"`
		}
		json.NewDecoder(r.Body).Decode(&in)
		g.gqlQueries = append(g.gqlQueries, in.Query)
		data := map[string]any{}
		for _, m := range regexp.MustCompile(`(r\d+):repository\(owner:"([^"]+)",name:"([^"]+)"\)`).FindAllStringSubmatch(in.Query, -1) {
			p, ok := g.perms[m[2]+"/"+m[3]]
			if !ok || p == "none" { // a repository the viewer cannot see is null
				data[m[1]] = nil
				continue
			}
			data[m[1]] = map[string]string{"viewerPermission": strings.ToUpper(p)}
		}
		json.NewEncoder(w).Encode(map[string]any{"data": data})
	})
	mux.HandleFunc("/repos/", func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		defer g.mu.Unlock()
		g.restCalls++
		if r.Header.Get("Authorization") != "Bearer "+visitorToken {
			http.Error(w, "bad", http.StatusUnauthorized)
			return
		}
		if g.restStatus != 0 {
			http.Error(w, "forced", g.restStatus)
			return
		}
		p, ok := g.perms[strings.TrimPrefix(r.URL.Path, "/repos/")]
		if !ok || p == "none" {
			http.NotFound(w, r)
			return
		}
		rank := map[string]int{"read": 1, "triage": 2, "write": 3, "maintain": 4, "admin": 5}[p]
		json.NewEncoder(w).Encode(map[string]any{"permissions": map[string]bool{"pull": rank >= 1, "triage": rank >= 2,
			"push": rank >= 3, "maintain": rank >= 4, "admin": rank >= 5}})
	})
	g.Server = httptest.NewServer(mux)
	t.Cleanup(g.Close)
	return g
}

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

// fixture builds one state directory and one root holding two repositories, an open one and a
// hidden one, with something of each in every endpoint that is keyed by repository.
func fixture(t *testing.T) (state, root string, targets map[string]map[string]string) {
	t.Helper()
	state, root = t.TempDir(), t.TempDir()
	put := func(path, body string) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	put(filepath.Join(root, "awf-gh.yaml"), "repos:\n  - repo: "+openRepo+"\n    scope: s\n    workflow: review-pr\n  - repo: "+hiddenRepo+"\n    scope: s\n    workflow: review-pr\n")
	put(filepath.Join(state, "reviews", "pr1-aaaaaaa", "repo"), openRepo)
	put(filepath.Join(state, "reviews", "pr1-aaaaaaa", "meta.json"), `{"title":"open review title"}`)
	put(filepath.Join(state, "reviews", "pr2-bbbbbbb", "repo"), hiddenRepo)
	put(filepath.Join(state, "reviews", "pr2-bbbbbbb", "meta.json"), `{"title":"hidden review title"}`)
	put(filepath.Join(state, "replies", "pr3-x", "pr.json"), `{"title":"open reply title","url":"https://github.com/`+openRepo+`/pull/3"}`)
	put(filepath.Join(state, "replies", "pr4-y", "pr.json"), `{"title":"hidden reply title","url":"https://github.com/`+hiddenRepo+`/pull/4"}`)
	put(filepath.Join(state, "awf-gh", "seen.jsonl"), openRepo+"#1\n"+hiddenRepo+"#2\n")
	put(filepath.Join(state, "tend", "acme", "app", "lessons.md"), "open lesson")
	put(filepath.Join(state, "tend", "acme", "client", "memory.md"), "hidden memory")
	put(filepath.Join(state, "asks", "acme", "app", "pr1", "thread.json"), `{"messages":[{"role":"user","text":"open question","at":"t"}]}`)
	put(filepath.Join(state, "asks", "acme", "client", "pr2", "thread.json"), `{"messages":[{"role":"user","text":"hidden question","at":"t"}]}`)
	put(filepath.Join(state, "notes", "20260101T000000Z-comment-open.txt"), "action: comment\nref: "+openRepo+"#1\nautonomy: observe\nclass: 1\nartifact: none\nreason: open note\n")
	put(filepath.Join(state, "held", "20260101T000001Z-comment-hidden.txt"), "action: comment\nref: "+hiddenRepo+"#2\nautonomy: propose\nclass: 1\nartifact: held/artifacts/x.md\nreason: hidden held\n")
	put(filepath.Join(state, "held", "artifacts", "x.md"), "hidden artifact body")
	// Two runs, one per repository, each with a price.
	step := `{"seq":4,"type":"step.completed","payload":{"name":"say","kind":"agent","cost_usd":0.5}}` + "\n"
	idx := ""
	targets = map[string]map[string]string{}
	var idxSb194 strings.Builder
	for id, repo := range map[string]string{"run-open-0001": openRepo, "run-hidden-02": hiddenRepo} {
		put(filepath.Join(state, "logs", id+".jsonl"), "prompt "+repo+"#1\n"+step)
		idxSb194.WriteString(`{"execution_id":"` + id + `","workflow_name":"review-pr","started_at":"2026-11-23T09:00:00Z"}` + "\n")
		targets[id] = map[string]string{"repo": repo, "pr": "1"}
	}
	idx += idxSb194.String()
	put(filepath.Join(state, "logs", "index.jsonl"), idx)

	st, err := store.Open(filepath.Join(state, "dots.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	a, _ := st.EnsureActor(ctx, store.Actor{Kind: "human", DisplayName: "Ada"})
	p, _, _ := st.UpsertProject(ctx, "default", openRepo, 0, nil)
	c, _, _ := st.UpsertProject(ctx, "default", hiddenRepo, 0, nil)
	w1, _ := st.CreateWorkItem(ctx, p.ID, "open one", a.ID, "github:acme/app#1")
	w2, _ := st.CreateWorkItem(ctx, p.ID, "open two", a.ID, "jira:ABC-2")
	w3, _ := st.CreateWorkItem(ctx, c.ID, "hidden three", a.ID, "sentry:s-3")
	if _, err := st.ProposeLink(ctx, store.LinkInput{A: w1.ID, B: w2.ID, Confidence: 0.8, Reason: "same trace", ProposedBy: "tend-repo"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ProposeLink(ctx, store.LinkInput{A: w1.ID, B: w3.ID, Confidence: 0.6, ProposedBy: "tend-repo"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.TakeClaim(ctx, w1.ID, a.ID, "laptop", "open-branch", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := st.TakeClaim(ctx, w3.ID, a.ID, "laptop", "hidden-branch", 0); err != nil {
		t.Fatal(err)
	}
	for _, e := range []struct{ target string }{{openRepo + "#1"}, {hiddenRepo + "#2"}, {"workflows/x.yaml"}} {
		if err := st.RecordAudit(ctx, "POST /api/ask", e.target, "local", 200); err != nil {
			t.Fatal(err)
		}
	}
	return state, root, targets
}

func newDash(state, root string, targets map[string]map[string]string, a *auth.Auth) *dash {
	d := &dash{state: state, root: root, awfgh: filepath.Join(root, "awf-gh.yaml"), allow: map[string]bool{"box.ts.net": true},
		rec: runrecorder.New(state, "", nil), auth: a, wtokens: workertokens.New(""), queue: jobs.Open(state)}
	d.targets.m = map[string]memoEntry[map[string]string]{}
	for id, in := range targets {
		d.targets.m[id] = memoEntry[map[string]string]{val: in}
	}
	return d
}

func testAuth(g *fakeGitHub, minPermission string, c *clock) *auth.Auth {
	return auth.New(auth.Config{ClientID: "client-1", GitHubURL: g.URL, APIURL: g.URL, MinPermission: minPermission, Now: c.now})
}

// ask sends a request through the real routes. A visitor comes through `tailscale serve`
// (a tailnet host and a login header); the owner reaches loopback directly.
func ask(h http.Handler, method, path, who, cookie, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:50000"
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if who == "visitor" {
		req.Host = "box.ts.net"
		req.Header.Set(tailscaleLoginHeader, "alice@example.com")
	} else {
		req.Host = "127.0.0.1:8099"
	}
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: cookie})
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

type rig struct {
	t       *testing.T
	gh      *fakeGitHub
	clk     *clock
	d       *dash
	h       http.Handler
	cookie  string
	targets map[string]map[string]string
}

func newRig(t *testing.T) *rig { return newRigMin(t, "") }

// newRigMin signs alice in against a dot that asks for minPermission. She can push to the open
// repository and only read the hidden one.
func newRigMin(t *testing.T, minPermission string) *rig {
	t.Helper()
	state, root, targets := fixture(t)
	g := newFakeGitHub(t)
	g.perms[openRepo] = "write"
	g.perms[hiddenRepo] = "read"
	clk := &clock{t: time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)}
	d := newDash(state, root, targets, testAuth(g, minPermission, clk))
	return &rig{t: t, gh: g, clk: clk, d: d, h: routes(d, filepath.Join(root, "index.html"), nil), targets: targets}
}

// signIn runs the whole device flow and keeps the session cookie.
func (r *rig) signIn() {
	r.t.Helper()
	start := ask(r.h, "POST", "/auth/device/start", "visitor", "", "{}")
	if start.Code != 200 {
		r.t.Fatalf("start = %d %s", start.Code, start.Body)
	}
	var s struct {
		ID string `json:"id"`
	}
	json.Unmarshal(start.Body.Bytes(), &s)
	r.gh.mu.Lock()
	r.gh.tokenReply = []map[string]any{{"access_token": visitorToken, "token_type": "bearer"}}
	r.gh.mu.Unlock()
	r.clk.advance(6 * time.Second)
	poll := ask(r.h, "POST", "/auth/device/poll", "visitor", "", `{"id":"`+s.ID+`"}`)
	if poll.Code != 200 || !strings.Contains(poll.Body.String(), `"ok"`) {
		r.t.Fatalf("poll = %d %s", poll.Code, poll.Body)
	}
	for _, c := range poll.Result().Cookies() {
		if c.Name == auth.CookieName {
			r.cookie = c.Value
		}
	}
	if r.cookie == "" {
		r.t.Fatal("no session cookie")
	}
}

func (r *rig) visitor(path string) *httptest.ResponseRecorder {
	return ask(r.h, "GET", path, "visitor", r.cookie, "")
}

func (r *rig) owner(path string) *httptest.ResponseRecorder {
	return ask(r.h, "GET", path, "owner", "", "")
}

func logTo(t *testing.T) *bytes.Buffer {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	return &buf
}

func TestDeviceFlowSignsAVisitorIn(t *testing.T) {
	logs := logTo(t)
	r := newRig(t)

	start := ask(r.h, "POST", "/auth/device/start", "visitor", "", "{}")
	if start.Code != 200 {
		t.Fatalf("start = %d %s", start.Code, start.Body)
	}
	var s map[string]any
	json.Unmarshal(start.Body.Bytes(), &s)
	if s["user_code"] != "ABCD-1234" || s["verification_uri"] == "" || s["id"] == "" || s["interval"].(float64) != 5 || s["expires_in"].(float64) != 900 {
		t.Fatalf("start = %v", s)
	}
	if strings.Contains(start.Body.String(), "devcode-secret") {
		t.Fatal("the device code reached the browser")
	}
	if len(r.gh.scopes) != 1 || r.gh.scopes[0] != "repo" {
		t.Fatalf("scopes sent to GitHub = %q, want the scope repo", r.gh.scopes)
	}
	id := s["id"].(string)
	poll := func() map[string]any {
		rec := ask(r.h, "POST", "/auth/device/poll", "visitor", "", `{"id":"`+id+`"}`)
		var out map[string]any
		json.Unmarshal(rec.Body.Bytes(), &out)
		return out
	}

	// Before the interval has passed GitHub is not asked.
	if out := poll(); out["status"] != "pending" || r.gh.tokenCalls != 0 {
		t.Fatalf("early poll = %v after %d GitHub calls", out, r.gh.tokenCalls)
	}
	r.clk.advance(6 * time.Second)
	if out := poll(); out["status"] != "pending" || r.gh.tokenCalls != 1 {
		t.Fatalf("pending poll = %v after %d GitHub calls", out, r.gh.tokenCalls)
	}
	r.gh.mu.Lock()
	r.gh.tokenReply = []map[string]any{{"access_token": visitorToken}}
	r.gh.mu.Unlock()
	r.clk.advance(6 * time.Second)
	rec := ask(r.h, "POST", "/auth/device/poll", "visitor", "", `{"id":"`+id+`"}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"login":"alice"`) {
		t.Fatalf("approved poll = %d %s", rec.Code, rec.Body)
	}
	var cookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.CookieName {
			cookie = c
		}
	}
	if cookie == nil || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Secure || len(cookie.Value) != 64 || cookie.MaxAge != 3600 {
		t.Fatalf("cookie = %+v", cookie)
	}
	if r.gh.userCalls != 1 {
		t.Fatalf("GET /user was called %d times, want once", r.gh.userCalls)
	}

	// The token is in no response, no cookie, no session, no log, nothing the dashboard keeps.
	for name, text := range map[string]string{
		"response": rec.Body.String(), "cookie": cookie.String(), "log": logs.String(),
		"auth state": r.d.auth.Dump(),
	} {
		if strings.Contains(text, visitorToken) {
			t.Errorf("the token is in the %s: %s", name, text)
		}
	}
	if s, _ := r.d.auth.Lookup(cookie.Value); s.Login != "alice" || !s.Expires.Equal(r.clk.now().Add(time.Hour)) ||
		len(s.Visible) != 1 || !s.Visible[openRepo] {
		t.Fatalf("session = %+v", s)
	}
	if r.gh.gqlCalls != 1 || r.gh.restCalls != 0 {
		t.Fatalf("GitHub saw %d GraphQL and %d REST calls, want one GraphQL", r.gh.gqlCalls, r.gh.restCalls)
	}
	if r.d.auth.PendingCount() != 0 {
		t.Fatal("the finished sign-in is still pending")
	}

	// Over https the cookie is Secure.
	req := httptest.NewRequest(http.MethodPost, "/auth/logout", strings.NewReader("{}"))
	req.RemoteAddr, req.Host = "127.0.0.1:1", "box.ts.net"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-Proto", "https")
	out := httptest.NewRecorder()
	r.h.ServeHTTP(out, req)
	if cs := out.Result().Cookies(); len(cs) != 1 || !cs[0].Secure {
		t.Fatalf("logout cookie = %+v", cs)
	}
}

func TestDevicePollOutcomes(t *testing.T) {
	start := func(r *rig) string {
		var s struct {
			ID string `json:"id"`
		}
		json.Unmarshal(ask(r.h, "POST", "/auth/device/start", "visitor", "", "{}").Body.Bytes(), &s)
		return s.ID
	}
	poll := func(r *rig, id string) (int, map[string]any) {
		rec := ask(r.h, "POST", "/auth/device/poll", "visitor", "", `{"id":"`+id+`"}`)
		var out map[string]any
		json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}

	t.Run("slow_down waits longer", func(t *testing.T) {
		r := newRig(t)
		id := start(r)
		r.gh.tokenReply = []map[string]any{{"error": "slow_down"}}
		r.clk.advance(6 * time.Second)
		if _, out := poll(r, id); out["status"] != "pending" || out["interval"].(float64) != 10 {
			t.Fatalf("slow_down answer = %v, want pending with a 10 second interval", out)
		}
		r.clk.advance(6 * time.Second) // 6 < 10: GitHub is not asked
		calls := r.gh.tokenCalls
		poll(r, id)
		if r.gh.tokenCalls != calls {
			t.Fatal("polled GitHub inside the slowed interval")
		}
		r.clk.advance(5 * time.Second)
		poll(r, id)
		if r.gh.tokenCalls != calls+1 {
			t.Fatal("did not poll once the slowed interval passed")
		}
	})
	t.Run("slow_down with GitHub's interval", func(t *testing.T) {
		r := newRig(t)
		id := start(r)
		r.gh.tokenReply = []map[string]any{{"error": "slow_down", "interval": 15}}
		r.clk.advance(6 * time.Second)
		if _, out := poll(r, id); out["interval"].(float64) != 15 {
			t.Fatalf("answer = %v", out)
		}
	})
	t.Run("expiry", func(t *testing.T) {
		r := newRig(t)
		id := start(r)
		r.clk.advance(901 * time.Second)
		if _, out := poll(r, id); out["status"] != "expired" || r.gh.tokenCalls != 0 {
			t.Fatalf("answer = %v after %d GitHub calls", out, r.gh.tokenCalls)
		}
		if code, _ := poll(r, id); code != 404 {
			t.Fatalf("a finished sign-in answered %d, want 404", code)
		}
	})
	t.Run("expired_token from GitHub", func(t *testing.T) {
		r := newRig(t)
		id := start(r)
		r.gh.tokenReply = []map[string]any{{"error": "expired_token"}}
		r.clk.advance(6 * time.Second)
		if _, out := poll(r, id); out["status"] != "expired" {
			t.Fatalf("answer = %v", out)
		}
	})
	t.Run("denied", func(t *testing.T) {
		r := newRig(t)
		id := start(r)
		r.gh.tokenReply = []map[string]any{{"error": "access_denied"}}
		r.clk.advance(6 * time.Second)
		if _, out := poll(r, id); out["status"] != "denied" {
			t.Fatalf("answer = %v", out)
		}
		if r.d.auth.SessionCount() != 0 {
			t.Fatal("a denied sign-in opened a session")
		}
	})
	t.Run("an unknown id", func(t *testing.T) {
		r := newRig(t)
		if code, _ := poll(r, "nope"); code != 404 {
			t.Fatalf("answered %d", code)
		}
	})
}

func TestSignInEndpointsCheckTheRequest(t *testing.T) {
	r := newRig(t)
	do := func(method, host, ctype, body string) int {
		req := httptest.NewRequest(method, "/auth/device/start", strings.NewReader(body))
		req.RemoteAddr, req.Host = "127.0.0.1:1", host
		if ctype != "" {
			req.Header.Set("Content-Type", ctype)
		}
		rec := httptest.NewRecorder()
		r.h.ServeHTTP(rec, req)
		return rec.Code
	}
	if c := do("GET", "box.ts.net", "application/json", ""); c != 405 {
		t.Errorf("GET = %d", c)
	}
	if c := do("POST", "evil.example", "application/json", "{}"); c != 403 {
		t.Errorf("untrusted host = %d", c)
	}
	if c := do("POST", "box.ts.net", "text/plain", "{}"); c != 415 {
		t.Errorf("not JSON = %d", c)
	}
	if c := do("POST", "box.ts.net", "application/json", `{"x":"`+strings.Repeat("a", 5000)+`"}`); c != 400 {
		t.Errorf("oversize = %d", c)
	}
	// 4 refused requests above did not reach the limiter before the checks; the limit is 10 a minute.
	got := 0
	for range 12 {
		if do("POST", "box.ts.net", "application/json", "{}") == 429 {
			got++
		}
	}
	if got == 0 {
		t.Error("start is not rate limited")
	}
	r.clk.advance(time.Minute + time.Second)
	if c := do("POST", "box.ts.net", "application/json", "{}"); c != 200 {
		t.Errorf("after the window = %d", c)
	}
}

// threeRepos gives the dot a third repository, and alice push on the first, read on the second
// and nothing on the third.
func threeRepos(t *testing.T, r *rig) {
	t.Helper()
	third := "acme/other"
	cfg := "repos:\n"
	var cfgSb558 strings.Builder
	for _, repo := range []string{openRepo, hiddenRepo, third} {
		cfgSb558.WriteString("  - repo: " + repo + "\n    scope: s\n    workflow: review-pr\n")
	}
	cfg += cfgSb558.String()
	if err := os.WriteFile(filepath.Join(r.d.root, "awf-gh.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	r.gh.perms[third] = "none"
}

func names(r *rig) string {
	var got []string
	json.Unmarshal(r.visitor("/api/repos/names").Body.Bytes(), &got)
	return strings.Join(got, ",")
}

func TestMinPermissionDecidesWhatAVisitorSees(t *testing.T) {
	for _, c := range []struct{ min, want string }{
		{"", openRepo},
		{"push", openRepo},
		{"pull", openRepo + "," + hiddenRepo},
	} {
		t.Run("min "+c.min, func(t *testing.T) {
			r := newRigMin(t, c.min)
			threeRepos(t, r)
			r.signIn()
			if got := names(r); got != c.want {
				t.Fatalf("names = %q, want %q", got, c.want)
			}
			var info map[string]any
			json.Unmarshal(r.visitor("/api/session").Body.Bytes(), &info)
			want := c.min
			if want == "" {
				want = "push"
			}
			if info["min_permission"] != want {
				t.Fatalf("session = %v", info)
			}
		})
	}
}

func TestEveryPermissionLevelIsMappedOnBothPaths(t *testing.T) {
	cases := map[string][2]bool{ // permission -> visible under push, under pull
		"read": {false, true}, "triage": {false, true}, "write": {true, true}, "maintain": {true, true}, "admin": {true, true}, "none": {false, false},
	}
	for _, rest := range []bool{false, true} {
		for perm, want := range cases {
			for i, min := range []string{"push", "pull"} {
				r := newRigMin(t, min)
				r.gh.perms[openRepo] = perm
				if rest {
					r.gh.gqlStatus = 500
				}
				r.signIn()
				if s, _ := r.d.auth.Lookup(r.cookie); s.Visible[openRepo] != want[i] {
					t.Errorf("rest=%v %s under %s: visible %v, want %v", rest, perm, min, s.Visible[openRepo], want[i])
				}
			}
		}
	}
}

func TestGraphQLFailureFallsBackToREST(t *testing.T) {
	for _, status := range []int{500, 403} {
		r := newRig(t)
		r.gh.gqlStatus = status
		r.signIn()
		if r.gh.restCalls != 2 {
			t.Fatalf("status %d: %d REST calls, want one per repository", status, r.gh.restCalls)
		}
		if got := names(r); got != openRepo {
			t.Fatalf("status %d: names = %q", status, got)
		}
	}
}

func TestGraphQLAsksForEveryRepositoryInChunks(t *testing.T) {
	r := newRig(t)
	var repos []string
	for i := range 120 {
		repos = append(repos, fmt.Sprintf("acme/r%d", i))
		r.gh.perms[repos[i]] = "write"
	}
	got := r.d.auth.VisibleRepos(visitorToken, repos)
	if len(got) != 120 || r.gh.gqlCalls != 3 {
		t.Fatalf("%d visible after %d GraphQL calls, want 120 after 3", len(got), r.gh.gqlCalls)
	}
	if q := r.gh.gqlQueries[0]; !strings.Contains(q, `r0:repository(owner:"acme",name:"r0"){viewerPermission}`) || strings.Count(q, "repository(") != 50 {
		t.Fatalf("query = %s", q)
	}
}

func TestAGitHubFailureShowsAVisitorNothing(t *testing.T) {
	t.Run("both APIs fail", func(t *testing.T) {
		r := newRig(t)
		r.gh.gqlStatus, r.gh.restStatus = 500, 500
		r.signIn()
		for _, p := range []string{"/api/repos", "/api/repos/names", "/api/memory", "/api/logs", "/api/claims"} {
			if body := strings.TrimSpace(r.visitor(p).Body.String()); body != "[]" {
				t.Errorf("%s = %s after a GitHub failure", p, body)
			}
		}
		if rec := r.visitor("/api/log?id=run-open-0001"); rec.Code != 404 {
			t.Errorf("a run answered %d after a GitHub failure", rec.Code)
		}
	})
	t.Run("GitHub returns no repository", func(t *testing.T) {
		r := newRig(t)
		r.gh.perms = map[string]string{} // nothing returned: hidden
		r.signIn()
		if got := names(r); got != "" {
			t.Fatalf("names = %q", got)
		}
	})
	t.Run("a 403 hides a repository", func(t *testing.T) {
		r := newRig(t)
		r.gh.gqlStatus, r.gh.restStatus = 500, 403
		r.signIn()
		if got := names(r); got != "" {
			t.Fatalf("names = %q", got)
		}
	})
}

func TestAnInvalidMinPermissionIsRefused(t *testing.T) {
	for _, bad := range []string{"admin", "write", "read", "none", "pul"} {
		if _, err := auth.ParseMinPermission(bad); err == nil || !strings.Contains(err.Error(), "push or pull") {
			t.Errorf("%q accepted or unclear: %v", bad, err)
		}
	}
	for _, good := range []string{"", "push", "pull", " Pull "} {
		if _, err := auth.ParseMinPermission(good); err != nil {
			t.Errorf("%q refused: %v", good, err)
		}
	}
}

func TestOwnerRequest(t *testing.T) {
	cases := []struct {
		name, host, remote, header string
		want                       bool
	}{
		{"direct on loopback", "127.0.0.1:8099", "127.0.0.1:5000", "", true},
		{"localhost", "localhost:8099", "[::1]:5000", "", true},
		{"tailnet login header", "127.0.0.1:8099", "127.0.0.1:5000", "a@example.com", false},
		{"tailnet host", "box.ts.net", "127.0.0.1:5000", "", false},
		{"remote address", "127.0.0.1:8099", "10.0.0.2:5000", "", false},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Host, req.RemoteAddr = c.host, c.remote
		if c.header != "" {
			req.Header.Set(tailscaleLoginHeader, c.header)
		}
		if got := ownerRequest(req); got != c.want {
			t.Errorf("%s: owner = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestAVisitorWhoHasNotSignedInSeesNothing(t *testing.T) {
	r := newRig(t)
	for _, p := range []string{"/api/repos", "/api/usage", "/api/audit", "/api/log?id=run-open-0001", "/api/files", "/api/health"} {
		rec := r.visitor(p)
		if rec.Code != 401 || !strings.Contains(rec.Body.String(), `"signin":true`) || strings.Contains(rec.Body.String(), "acme") {
			t.Errorf("%s = %d %s", p, rec.Code, rec.Body)
		}
	}
	if rec := r.visitor("/api/session"); !strings.Contains(rec.Body.String(), `"anonymous"`) {
		t.Errorf("session = %s", rec.Body)
	}
	// The page itself still loads: it holds no data and shows the sign-in.
	os.WriteFile(filepath.Join(r.d.root, "index.html"), []byte("<html>shell</html>"), 0o644)
	if rec := r.visitor("/"); rec.Code != 200 {
		t.Errorf("the shell = %d", rec.Code)
	}
}

func TestVisitorSeesOnlyThePermittedRepository(t *testing.T) {
	r := newRig(t)
	r.signIn()
	want := map[string][]string{
		"/api/artifacts":   {"open review title", "open reply title", openRepo + `","pr":"1"`},
		"/api/repos":       {openRepo},
		"/api/repos/names": {openRepo},
		"/api/memory":      {"open lesson"},
		"/api/logs":        {"run-open-0001"},
		"/api/usage":       {openRepo},
		"/api/claims":      {"open-branch"},
		"/api/links":       {"open one", "open two"},
		"/api/autonomy":    {"open note"},
		"/api/audit":       {openRepo + "#1"},
	}
	for path, musts := range want {
		rec := r.visitor(path)
		if rec.Code != 200 {
			t.Errorf("%s = %d %s", path, rec.Code, rec.Body)
			continue
		}
		body := rec.Body.String()
		for _, m := range musts {
			if !strings.Contains(body, m) {
				t.Errorf("%s lost %q: %s", path, m, body)
			}
		}
		for _, leak := range []string{hiddenRepo, "hidden", "run-hidden-02", "acme/client", "workflows/x.yaml"} {
			if strings.Contains(body, leak) {
				t.Errorf("%s leaks %q: %s", path, leak, body)
			}
		}
	}

	// The total is recomputed from what the visitor may see.
	var usage struct {
		Total  float64
		Runs   int
		Groups []usageOne
	}
	json.Unmarshal(r.visitor("/api/usage").Body.Bytes(), &usage)
	var full struct {
		Total float64
		Runs  int
	}
	json.Unmarshal(r.owner("/api/usage").Body.Bytes(), &full)
	if full.Runs != 2 || full.Total != 1 || usage.Runs != 1 || usage.Total != 0.5 || len(usage.Groups) != 1 {
		t.Fatalf("usage: owner %+v, visitor %+v", full, usage)
	}

	// One run, one conversation, one artifact by name: the open ones answer, the hidden ones
	// answer exactly as a record that does not exist.
	for _, p := range []string{"/api/log?id=run-open-0001", "/api/cost?id=run-open-0001", "/api/thread?repo=" + openRepo + "&pr=1",
		"/api/file?path=reviews/pr1-aaaaaaa/meta.json", "/api/replay?id=run-open-0001"} {
		if rec := r.visitor(p); rec.Code != 200 {
			t.Errorf("%s = %d %s", p, rec.Code, rec.Body)
		}
	}
	missing := r.visitor("/api/log?id=run-nothing-99")
	for _, p := range []string{"/api/log?id=run-hidden-02", "/api/cost?id=run-hidden-02", "/api/run?id=run-hidden-02", "/api/stream?id=run-hidden-02",
		"/api/sessions?id=run-hidden-02", "/api/replay?id=run-hidden-02",
		"/api/file?path=reviews/pr2-bbbbbbb/meta.json", "/api/file?path=replies/pr4-y/pr.json"} {
		rec := r.visitor(p)
		if rec.Code != 404 || rec.Body.String() != missing.Body.String() {
			t.Errorf("%s = %d %s, want the answer of a missing record", p, rec.Code, rec.Body)
		}
	}
	nothing := r.visitor("/api/thread?repo=acme/nothing&pr=1")
	if rec := r.visitor("/api/thread?repo=" + hiddenRepo + "&pr=2"); rec.Code != 404 || rec.Body.String() != nothing.Body.String() {
		t.Errorf("hidden thread = %d %s", rec.Code, rec.Body)
	}
	if rec := r.visitor("/api/autonomy/artifact?kind=held&name=20260101T000001Z-comment-hidden.txt"); rec.Code != 404 || strings.Contains(rec.Body.String(), "hidden artifact") {
		t.Errorf("hidden artifact = %d %s", rec.Code, rec.Body)
	}

	// The owner sees both, unchanged.
	for _, p := range []string{"/api/repos", "/api/memory", "/api/logs", "/api/claims", "/api/links", "/api/autonomy", "/api/artifacts"} {
		body := r.owner(p).Body.String()
		if !strings.Contains(body, hiddenRepo) && !strings.Contains(body, "hidden") {
			t.Errorf("the owner lost the hidden repository in %s: %s", p, body)
		}
	}
	if rec := r.owner("/api/log?id=run-hidden-02"); rec.Code != 200 {
		t.Errorf("the owner reads a hidden run: %d", rec.Code)
	}
}

func TestVisitorSeesNoLinkAcrossTheLine(t *testing.T) {
	r := newRig(t)
	r.signIn()
	var open, all []linkView
	json.Unmarshal(r.visitor("/api/links").Body.Bytes(), &open)
	json.Unmarshal(r.owner("/api/links").Body.Bytes(), &all)
	if len(all) != 2 || len(open) != 1 || open[0].A.Repo != openRepo || open[0].B.Repo != openRepo {
		t.Fatalf("links: owner %d, visitor %+v", len(all), open)
	}
}

func TestVisitorAuditShowsOnlyVisibleRepositories(t *testing.T) {
	r := newRig(t)
	r.signIn()
	var rows []auditView
	json.Unmarshal(r.visitor("/api/audit?limit=1").Body.Bytes(), &rows)
	if len(rows) != 1 || rows[0].Target != openRepo+"#1" {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestVisitorCannotUseOwnerEndpoints(t *testing.T) {
	r := newRig(t)
	r.signIn()
	cases := []struct{ method, path string }{
		{"PUT", "/api/save"}, {"GET", "/api/files"}, {"GET", "/api/source?path=repos.yaml"}, {"GET", "/api/health"},
		{"GET", "/api/workers"}, {"GET", "/api/models"}, {"PUT", "/api/models"}, {"GET", "/api/notifications"},
		{"PUT", "/api/notifications"}, {"PATCH", "/api/repos/config"}, {"POST", "/api/autonomy/release"},
		{"POST", "/api/links/accept"}, {"POST", "/api/links/decline"}, {"POST", "/api/links/split"},
		{"POST", "/api/ask"}, {"POST", "/api/ask/answer"}, {"GET", "/awf/api/history"}, {"POST", "/awf/api/workflows/x/run"},
	}
	for _, c := range cases {
		body := ""
		if c.method != "GET" {
			body = "{}"
		}
		rec := ask(r.h, c.method, c.path, "visitor", r.cookie, body)
		if rec.Code != 403 {
			t.Errorf("%s %s = %d %s, want 403", c.method, c.path, rec.Code, rec.Body)
		}
	}
	// Refused writes are rows with the GitHub login; so is a refused read.
	st, err := store.Open(filepath.Join(r.d.state, "dots.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	rows, _ := st.RecentAudit(context.Background(), 500)
	saw := map[string]bool{}
	for _, e := range rows {
		if e.Caller == "alice" {
			saw[e.Endpoint] = true
			if e.Outcome != 403 && e.Endpoint != "POST /auth/device/poll" {
				t.Errorf("row %+v is not a refusal", e)
			}
		}
	}
	for _, ep := range []string{"PUT /api/save", "POST /api/ask", "POST /api/links/accept", "GET /api/health", "GET /api/files", "POST /awf/", "POST /auth/device/poll"} {
		if !saw[ep] {
			t.Errorf("no audit row from alice for %s: %v", ep, saw)
		}
	}
	// A refused write from someone who has not signed in is a row too, named by the tailnet login the proxy forwards.
	ask(r.h, "PUT", "/api/save", "visitor", "", "{}")
	rows, _ = st.RecentAudit(context.Background(), 1)
	if len(rows) != 1 || rows[0].Caller != "alice@example.com" || rows[0].Outcome != 401 {
		t.Errorf("row = %+v", rows)
	}
}

func TestVisitorReadsAreAuditedWithTheLogin(t *testing.T) {
	r := newRig(t)
	r.signIn()
	r.visitor("/api/thread?repo=" + openRepo + "&pr=1")
	r.visitor("/api/thread?repo=" + openRepo + "&pr=1") // a poll: no second row
	st, _ := store.Open(filepath.Join(r.d.state, "dots.db"))
	defer st.Close()
	rows, _ := st.RecentAudit(context.Background(), 500)
	n := 0
	for _, e := range rows {
		if e.Endpoint == "GET /api/thread" && e.Caller == "alice" && e.Target == openRepo {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d audit rows for the read, want 1: %+v", n, rows)
	}
}

func TestLogoutEndsTheSession(t *testing.T) {
	r := newRig(t)
	r.signIn()
	if r.visitor("/api/repos").Code != 200 {
		t.Fatal("signed in but refused")
	}
	rec := ask(r.h, "POST", "/auth/logout", "visitor", r.cookie, "{}")
	if rec.Code != 200 || r.d.auth.SessionCount() != 0 {
		t.Fatalf("logout = %d, sessions %d", rec.Code, r.d.auth.SessionCount())
	}
	if r.visitor("/api/repos").Code != 401 {
		t.Fatal("the session outlived the logout")
	}
}

func TestSessionExpiresAfterOneHour(t *testing.T) {
	r := newRig(t)
	r.signIn()
	r.clk.advance(time.Hour - time.Minute)
	if r.visitor("/api/repos").Code != 200 {
		t.Fatal("the session ended early")
	}
	r.clk.advance(2 * time.Minute)
	if r.visitor("/api/repos").Code != 401 {
		t.Fatal("the session outlived one hour")
	}
}

// With no client id configured nothing changes: the same handlers answer the same bytes, and
// the sign-in routes are as missing as any other unknown path.
func TestSignInOffChangesNothing(t *testing.T) {
	state, root, targets := fixture(t)
	g := newFakeGitHub(t)
	clk := &clock{t: time.Now()}
	off := routes(newDash(state, root, targets, nil), filepath.Join(root, "index.html"), nil)
	on := routes(newDash(state, root, targets, testAuth(g, "", clk)), filepath.Join(root, "index.html"), nil)
	if auth.New(auth.Config{}) != nil {
		t.Fatal("no client id still builds the sign-in")
	}
	paths := []string{"/api/artifacts", "/api/repos", "/api/repos/names", "/api/memory", "/api/logs", "/api/usage", "/api/claims",
		"/api/links", "/api/autonomy", "/api/audit", "/api/workers", "/api/health", "/api/files", "/api/remote-runs",
		"/api/log?id=run-hidden-02", "/api/cost?id=run-hidden-02", "/api/thread?repo=" + hiddenRepo + "&pr=2",
		"/api/file?path=reviews/pr2-bbbbbbb/meta.json", "/api/autonomy/artifact?kind=held&name=20260101T000001Z-comment-hidden.txt"}
	for _, p := range paths {
		a, b := ask(off, "GET", p, "owner", "", ""), ask(on, "GET", p, "owner", "", "")
		if a.Code != b.Code || a.Body.String() != b.Body.String() || a.Header().Get("Content-Type") != b.Header().Get("Content-Type") {
			t.Errorf("%s differs: off %d %q, on %d %q", p, a.Code, a.Body, b.Code, b.Body)
		}
		if a.Code != 200 {
			t.Errorf("%s = %d with sign-in off", p, a.Code)
		}
	}
	// With it off a visitor request is served as before, and the sign-in routes do not exist.
	for _, p := range []string{"/api/repos", "/api/health"} {
		if rec := ask(off, "GET", p, "visitor", "", ""); rec.Code != 200 {
			t.Errorf("%s = %d for a tailnet request with sign-in off", p, rec.Code)
		}
	}
	for _, c := range []struct{ method, path string }{{"POST", "/auth/device/start"}, {"POST", "/auth/device/poll"}, {"POST", "/auth/logout"}, {"GET", "/api/session"}} {
		rec := ask(off, c.method, c.path, "owner", "", "{}")
		if rec.Code != 404 || rec.Body.String() != "404 page not found\n" {
			t.Errorf("%s %s = %d %q", c.method, c.path, rec.Code, rec.Body)
		}
	}
}

func TestAnOwnerKeepsTheFullViewWithSignInOn(t *testing.T) {
	r := newRig(t)
	var info map[string]any
	json.Unmarshal(r.owner("/api/session").Body.Bytes(), &info)
	if info["role"] != "owner" {
		t.Fatalf("session = %v", info)
	}
	if r.gh.gqlCalls+r.gh.restCalls != 0 {
		t.Fatal("the owner's reads asked GitHub")
	}
	if rec := ask(r.h, "GET", "/api/files", "owner", "", ""); rec.Code != 200 {
		t.Fatalf("owner files = %d", rec.Code)
	}
}
