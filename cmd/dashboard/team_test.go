package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

// teamAsk sends a request the way an SSH forward delivers it: from loopback, with a loopback
// Host and no tailnet header, to the team listener.
func teamAsk(h http.Handler, method, path, cookie, body string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:50000"
	req.Host = "127.0.0.1:18098"
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdr {
		if k == "Host" {
			req.Host = v
			continue
		}
		req.Header.Set(k, v)
	}
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// teamSignIn runs the device flow on the team listener and returns the cookie.
func teamSignIn(t *testing.T, r *rig, th http.Handler) string {
	t.Helper()
	start := teamAsk(th, "POST", "/auth/device/start", "", "{}", nil)
	if start.Code != 200 {
		t.Fatalf("start = %d %s", start.Code, start.Body)
	}
	var s struct {
		ID string `json:"id"`
	}
	json.Unmarshal(start.Body.Bytes(), &s)
	r.gh.mu.Lock()
	r.gh.tokenReply = []map[string]any{{"access_token": visitorToken, "token_type": "bearer"}}
	r.gh.mu.Unlock()
	r.clk.advance(6 * time.Second)
	poll := teamAsk(th, "POST", "/auth/device/poll", "", `{"id":"`+s.ID+`"}`, nil)
	if poll.Code != 200 || !strings.Contains(poll.Body.String(), `"ok"`) {
		t.Fatalf("poll = %d %s", poll.Code, poll.Body)
	}
	for _, c := range poll.Result().Cookies() {
		if c.Name == sessionCookie {
			return c.Value
		}
	}
	t.Fatal("no session cookie")
	return ""
}

func TestTeamListenerRefusesWhatItMust(t *testing.T) {
	for _, tc := range []struct {
		name, team, owner string
		signIn            bool
		ok                bool
	}{
		{"unset", "", "127.0.0.1:8099", false, true},
		{"loopback v4", "127.0.0.1:8098", "127.0.0.1:8099", true, true},
		{"loopback 127.0.0.2", "127.0.0.2:8098", "127.0.0.1:8099", true, true},
		{"loopback v6", "[::1]:8098", "127.0.0.1:8099", true, true},
		{"all interfaces", "0.0.0.0:8098", "127.0.0.1:8099", true, false},
		{"empty host", ":8098", "127.0.0.1:8099", true, false},
		{"public address", "192.0.2.10:8098", "127.0.0.1:8099", true, false},
		{"a name", "localhost:8098", "127.0.0.1:8099", true, false},
		{"no port", "127.0.0.1", "127.0.0.1:8099", true, false},
		{"no sign-in", "127.0.0.1:8098", "127.0.0.1:8099", false, false},
		{"owner address", "127.0.0.1:8099", "127.0.0.1:8099", true, false},
	} {
		err := checkTeamAddr(tc.team, tc.owner, tc.signIn)
		if (err == nil) != tc.ok {
			t.Errorf("%s: err = %v, want ok=%v", tc.name, err, tc.ok)
		}
	}
}

func TestTeamListenerNeverMakesAnOwner(t *testing.T) {
	r := newRig(t)
	th := teamHandler(r.h)
	// The same request: owner on the owner listener, anonymous on the team one.
	if got := r.owner("/api/session").Body.String(); !strings.Contains(got, `"owner"`) {
		t.Fatalf("owner listener changed: %s", got)
	}
	rec := teamAsk(th, "GET", "/api/session", "", "", nil)
	if !strings.Contains(rec.Body.String(), `"anonymous"`) || strings.Contains(rec.Body.String(), "owner") {
		t.Fatalf("team session = %s", rec.Body)
	}
	// Whatever the client sends cannot change that.
	for _, hdr := range []map[string]string{
		{"Host": "localhost:8099"},
		{"Host": "127.0.0.1:8099"},
		{"Host": "[::1]:1"},
		{"X-Forwarded-For": "127.0.0.1", "X-Forwarded-Proto": "https"},
	} {
		rec := teamAsk(th, "GET", "/api/session", "", "", hdr)
		if !strings.Contains(rec.Body.String(), `"anonymous"`) {
			t.Errorf("%v: %s", hdr, rec.Body)
		}
	}
	// The same request, directly on the owner routes, is the owner's.
	if rec := r.owner("/api/health"); rec.Code == 401 || rec.Code == 403 {
		t.Errorf("owner health = %d", rec.Code)
	}
}

func TestTeamListenerChecksHostAndOrigin(t *testing.T) {
	r := newRig(t)
	th := teamHandler(r.h)
	for _, host := range []string{"box.ts.net", "example.com", "192.0.2.1:8098", ""} {
		if rec := teamAsk(th, "GET", "/api/session", "", "", map[string]string{"Host": host}); rec.Code != 403 {
			t.Errorf("host %q = %d, want 403", host, rec.Code)
		}
	}
	// A tailnet name on the allow list is trusted by the owner listener, never by this one.
	if rec := teamAsk(th, "GET", "/api/session", "", "", map[string]string{"Host": "box.ts.net"}); rec.Code != 403 {
		t.Errorf("allowed host = %d", rec.Code)
	}
	if rec := teamAsk(th, "GET", "/api/session", "", "", map[string]string{"Host": "127.0.0.1:9", "Origin": "http://127.0.0.1:9"}); rec.Code != 200 {
		t.Errorf("any loopback port = %d", rec.Code)
	}
	for _, origin := range []string{"http://127.0.0.1:8099", "http://localhost:18098", "https://evil.example", "null"} {
		rec := teamAsk(th, "POST", "/auth/device/start", "", "{}", map[string]string{"Origin": origin})
		if rec.Code != 403 {
			t.Errorf("origin %q = %d, want 403", origin, rec.Code)
		}
	}
	if rec := teamAsk(th, "POST", "/auth/device/start", "", "{}", map[string]string{"Origin": "http://127.0.0.1:18098"}); rec.Code != 200 {
		t.Errorf("same origin = %d %s", rec.Code, rec.Body)
	}
}

func TestDeviceFlowWorksOnTheTeamListener(t *testing.T) {
	r := newRig(t)
	th := teamHandler(r.h)
	cookie := teamSignIn(t, r, th)
	rec := teamAsk(th, "GET", "/api/session", cookie, "", nil)
	if !strings.Contains(rec.Body.String(), `"visitor"`) || !strings.Contains(rec.Body.String(), "alice") {
		t.Fatalf("session = %s", rec.Body)
	}
	// Only what the visitor may see.
	got := teamAsk(th, "GET", "/api/repos/names", cookie, "", nil).Body.String()
	if !strings.Contains(got, openRepo) || strings.Contains(got, hiddenRepo) {
		t.Errorf("names = %s", got)
	}
}

func TestOwnerListenerAndSignInOffAreUnchanged(t *testing.T) {
	r := newRig(t)
	if rec := r.owner("/api/health"); rec.Code == 401 || rec.Code == 403 {
		t.Errorf("owner health = %d", rec.Code)
	}
	if got := r.owner("/api/session").Body.String(); !strings.Contains(got, `"owner"`) {
		t.Errorf("session = %s", got)
	}
	if teamRequest(httptest.NewRequest("GET", "/", nil)) {
		t.Error("a plain request is not a team request")
	}
}

// Every route registered in routes.go is either open to a signed-in visitor (and filtered by a
// rule) or owner only. A new route fails here until it is listed, and no route that changes
// state or is owner only may answer a team request as the owner.
func TestNoRouteIsReachableAsOwnerThroughTheTeamListener(t *testing.T) {
	visitorRoutes := map[string]bool{
		"/": true, "/auth/device/start": true, "/auth/device/poll": true, "/auth/logout": true,
		"/api/session": true, "/api/artifacts": true, "/api/file": true, "/api/log": true,
		"/api/stream": true, "/api/cost": true, "/api/home": true, "/api/usage": true,
		"/api/run": true, "/api/sessions": true, "/api/logs": true, "/api/remote-runs": true,
		"/api/repos": true, "/api/repos/names": true, "/api/replay": true, "/api/thread": true,
		"/api/memory": true, "/api/claims": true, "/api/audit": true, "/api/links": true,
		"/api/autonomy": true, "/api/autonomy/artifact": true, "/api/hub-token": true,
	}
	ownerRoutes := map[string]bool{
		"/api/repos/config": true, "/api/ask": true, "/api/ask/answer": true, "/api/files": true,
		"/api/models": true, "/api/links/accept": true, "/api/links/decline": true,
		"/api/links/split": true, "/api/workers": true, "/api/autonomy/release": true,
		"/api/health": true, "/api/notifications": true, "/api/source": true, "/api/save": true,
		"/awf/": true,
	}
	src, err := os.ReadFile("routes.go")
	if err != nil {
		t.Fatal(err)
	}
	found := regexp.MustCompile(`mux\.HandleFunc\("([^"]+)"`).FindAllStringSubmatch(string(src), -1)
	if len(found) < 40 {
		t.Fatalf("read only %d routes from routes.go", len(found))
	}
	r := newRig(t)
	r.d.awf = "http://127.0.0.1:1"
	th := teamHandler(routes(r.d, "ui-missing.html", nil))
	for _, m := range found {
		path := m[1]
		r.d.auth.mu.Lock()
		r.d.auth.limits = map[string]*window{} // sign-ins are rate limited
		r.d.auth.mu.Unlock()
		cookie := teamSignIn(t, r, th) // logout ends a session, so each route gets its own
		if !visitorRoutes[path] && !ownerRoutes[path] {
			t.Errorf("route %s is in neither list: decide whether a visitor may reach it", path)
			continue
		}
		for _, method := range []string{"GET", "POST", "PUT", "DELETE"} {
			for _, ck := range []string{"", cookie} {
				rec := teamAsk(th, method, path, ck, "{}", nil)
				switch {
				case ownerRoutes[path]:
					want := 403
					if ck == "" {
						want = 401
					}
					if rec.Code != want {
						t.Errorf("%s %s cookie=%v = %d, want %d: %s", method, path, ck != "", rec.Code, want, rec.Body)
					}
				case ck == "" && path != "/" && !strings.HasPrefix(path, "/auth/") && path != "/api/session":
					if rec.Code != 401 {
						t.Errorf("anonymous %s %s = %d, want 401", method, path, rec.Code)
					}
				}
			}
		}
	}
	// The same owner route, on the owner listener, is let through: the test would otherwise pass
	// by refusing everything.
	if rec := ask(r.h, "GET", "/api/health", "owner", "", ""); rec.Code == 401 || rec.Code == 403 {
		t.Errorf("owner = %d", rec.Code)
	}
}
