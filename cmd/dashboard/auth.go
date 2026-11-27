package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// GitHub sign-in for visitors (ADR 0044, first step, docs/dashboard-auth.md). It is off unless
// a GitHub OAuth client id is configured; with it off nothing here runs and the dashboard
// answers as it always did.
//
// The visitor proves who they are through the OAuth device flow with NO scope. The dashboard
// reads their login once, drops the token at once and keeps only the login in a server-side
// session. What a login may see is asked of GitHub with the dot's own token (visibility.go).

const (
	sessionCookie  = "dots_session"
	sessionTTL     = 8 * time.Hour
	maxSessions    = 500
	maxPending     = 200
	defaultPollGap = 5 * time.Second
	slowDownStep   = 5 * time.Second
	deviceGrant    = "urn:ietf:params:oauth:grant-type:device_code"
	authBodyCap    = 4096
	githubBodyCap  = 1 << 20
)

// loginRE is what a GitHub login looks like, checked before it reaches a URL path.
var loginRE = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// secret keeps the dot's own token out of a log line or a debug print.
type secret string

func (secret) String() string   { return "[redacted]" }
func (secret) GoString() string { return "[redacted]" }

// authConfig is what the flags and the environment give.
type authConfig struct {
	ClientID  string
	GitHubURL string // device flow host, default https://github.com
	APIURL    string // REST API, default https://api.github.com
	Token     string // the dot's own token, from GH_TOKEN
}

type authState struct {
	clientID  string
	githubURL string
	apiURL    string
	token     secret
	client    *http.Client
	now       func() time.Time

	mu       sync.Mutex
	pending  map[string]*devicePending
	sessions map[string]session
	perms    map[string]permEntry
	limits   map[string]*window
	readSeen map[string]time.Time
}

// devicePending is one device flow in progress. The device code never leaves the server.
type devicePending struct {
	deviceCode string
	interval   time.Duration
	nextPoll   time.Time
	expires    time.Time
}

// session holds a login and nothing else about the visitor.
type session struct {
	login   string
	expires time.Time
}

type window struct {
	start time.Time
	n     int
}

func newAuth(c authConfig) *authState {
	if c.ClientID == "" {
		return nil
	}
	return &authState{
		clientID:  c.ClientID,
		githubURL: strings.TrimRight(firstNonEmpty(c.GitHubURL, "https://github.com"), "/"),
		apiURL:    strings.TrimRight(firstNonEmpty(c.APIURL, "https://api.github.com"), "/"),
		token:     secret(c.Token),
		client:    &http.Client{Timeout: 10 * time.Second},
		now:       time.Now,
		pending:   map[string]*devicePending{},
		sessions:  map[string]session{},
		perms:     map[string]permEntry{},
		limits:    map[string]*window{},
		readSeen:  map[string]time.Time{},
	}
}

func randomID(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("no randomness: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// ownerRequest is the host's own user: the request reached the dashboard directly on
// loopback, with no tailnet login header. Anything `tailscale serve` forwards is a visitor.
func ownerRequest(r *http.Request) bool {
	if r.Header.Get(tailscaleLoginHeader) != "" || !isLoopbackHost(r.Host) {
		return false
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// sessionLogin is the login of the request's session, or "" when there is none.
func (a *authState) sessionLogin(r *http.Request) string {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return ""
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	s, ok := a.sessions[c.Value]
	if !ok {
		return ""
	}
	if !a.now().Before(s.expires) {
		delete(a.sessions, c.Value)
		return ""
	}
	return s.login
}

func (a *authState) openSession(login string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	for id, s := range a.sessions {
		if !now.Before(s.expires) {
			delete(a.sessions, id)
		}
	}
	for len(a.sessions) >= maxSessions { // drop the soonest to expire
		oldest, at := "", time.Time{}
		for id, s := range a.sessions {
			if oldest == "" || s.expires.Before(at) {
				oldest, at = id, s.expires
			}
		}
		delete(a.sessions, oldest)
	}
	id := randomID(32)
	a.sessions[id] = session{login: login, expires: now.Add(sessionTTL)}
	return id
}

// allow is a fixed window per client and kind: n requests a minute.
func (a *authState) allow(r *http.Request, kind string, n int) bool {
	key := kind + "|" + clientKey(r)
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	if len(a.limits) > 2000 {
		for k, w := range a.limits {
			if now.Sub(w.start) > time.Minute {
				delete(a.limits, k)
			}
		}
	}
	w := a.limits[key]
	if w == nil || now.Sub(w.start) >= time.Minute {
		a.limits[key] = &window{start: now, n: 1}
		return true
	}
	w.n++
	return w.n <= n
}

func clientKey(r *http.Request) string {
	if l := strings.TrimSpace(r.Header.Get(tailscaleLoginHeader)); l != "" {
		return l
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func secureRequest(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func authError(w http.ResponseWriter, status int, msg string) {
	writeJSONStatus(w, status, map[string]string{"error": msg})
}

// authPost runs the checks every sign-in endpoint shares and decodes the JSON body into in.
func (d *dash) authPost(w http.ResponseWriter, r *http.Request, kind string, perMinute int, in any) bool {
	if r.Method != http.MethodPost {
		authError(w, http.StatusMethodNotAllowed, "POST only")
		return false
	}
	if !d.trustedRequest(r) {
		authError(w, http.StatusForbidden, "loopback or an allowed host only")
		return false
	}
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		authError(w, http.StatusUnsupportedMediaType, "JSON only")
		return false
	}
	if !d.auth.allow(r, kind, perMinute) {
		w.Header().Set("Retry-After", "60")
		authError(w, http.StatusTooManyRequests, "too many requests")
		return false
	}
	if in != nil {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, authBodyCap)).Decode(in); err != nil && !errors.Is(err, io.EOF) {
			authError(w, http.StatusBadRequest, "bad request body")
			return false
		}
	}
	return true
}

// githubForm posts a form to the device flow host and decodes the JSON answer.
func (a *authState) githubForm(path string, form url.Values, out any) error {
	req, err := http.NewRequest(http.MethodPost, a.githubURL+path, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := a.client.Do(req)
	if err != nil {
		return errors.New("github unreachable")
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return errors.New("github answered " + strconv.Itoa(resp.StatusCode))
	}
	if json.NewDecoder(io.LimitReader(resp.Body, githubBodyCap)).Decode(out) != nil {
		return errors.New("github answered with something unreadable")
	}
	return nil
}

// deviceStart begins a device flow: POST /auth/device/start. The scope is empty on purpose:
// the visitor proves who they are and grants nothing.
func (d *dash) deviceStart(w http.ResponseWriter, r *http.Request) {
	if d.auth == nil {
		http.NotFound(w, r)
		return
	}
	var in struct{}
	if !d.authPost(w, r, "start", 10, &in) {
		return
	}
	a := d.auth
	var out struct {
		DeviceCode string `json:"device_code"`
		UserCode   string `json:"user_code"`
		URI        string `json:"verification_uri"`
		ExpiresIn  int    `json:"expires_in"`
		Interval   int    `json:"interval"`
	}
	if err := a.githubForm("/login/device/code", url.Values{"client_id": {a.clientID}, "scope": {""}}, &out); err != nil ||
		out.DeviceCode == "" || out.UserCode == "" || out.ExpiresIn <= 0 {
		log.Printf("sign-in: the device code request failed")
		authError(w, http.StatusBadGateway, "GitHub did not start the sign-in")
		return
	}
	interval := time.Duration(out.Interval) * time.Second
	if interval < time.Second {
		interval = defaultPollGap
	}
	now := a.now()
	id := randomID(16)
	a.mu.Lock()
	for k, p := range a.pending {
		if !now.Before(p.expires) {
			delete(a.pending, k)
		}
	}
	if len(a.pending) >= maxPending {
		a.mu.Unlock()
		authError(w, http.StatusTooManyRequests, "too many sign-ins in progress")
		return
	}
	a.pending[id] = &devicePending{deviceCode: out.DeviceCode, interval: interval, nextPoll: now.Add(interval),
		expires: now.Add(time.Duration(out.ExpiresIn) * time.Second)}
	a.mu.Unlock()
	writeJSON(w, map[string]any{"id": id, "user_code": out.UserCode, "verification_uri": out.URI,
		"interval": int(interval / time.Second), "expires_in": out.ExpiresIn})
}

// devicePoll asks GitHub whether the visitor approved: POST /auth/device/poll {"id"}. It
// answers pending, expired, denied or ok, and never the token.
func (d *dash) devicePoll(w http.ResponseWriter, r *http.Request) {
	if d.auth == nil {
		http.NotFound(w, r)
		return
	}
	var in struct {
		ID string `json:"id"`
	}
	if !d.authPost(w, r, "poll", 30, &in) {
		return
	}
	a := d.auth
	a.mu.Lock()
	p := a.pending[in.ID]
	now := a.now()
	if p == nil {
		a.mu.Unlock()
		authError(w, http.StatusNotFound, "no such sign-in")
		return
	}
	if !now.Before(p.expires) {
		delete(a.pending, in.ID)
		a.mu.Unlock()
		writeJSON(w, map[string]any{"status": "expired"})
		return
	}
	if now.Before(p.nextPoll) { // GitHub is not asked before the interval has passed
		interval := int(p.interval / time.Second)
		a.mu.Unlock()
		writeJSON(w, map[string]any{"status": "pending", "interval": interval})
		return
	}
	p.nextPoll = now.Add(p.interval)
	code := p.deviceCode
	a.mu.Unlock()

	var res struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
		Interval    int    `json:"interval"`
	}
	err := a.githubForm("/login/oauth/access_token", url.Values{"client_id": {a.clientID},
		"device_code": {code}, "grant_type": {deviceGrant}}, &res)
	if err != nil {
		log.Printf("sign-in: the token request failed")
		authError(w, http.StatusBadGateway, "GitHub did not answer")
		return
	}
	switch res.Error {
	case "":
	case "authorization_pending":
		writeJSON(w, map[string]any{"status": "pending", "interval": int(a.slowDown(in.ID, 0, false) / time.Second)})
		return
	case "slow_down":
		writeJSON(w, map[string]any{"status": "pending", "interval": int(a.slowDown(in.ID, res.Interval, true) / time.Second)})
		return
	case "expired_token":
		a.forget(in.ID)
		writeJSON(w, map[string]any{"status": "expired"})
		return
	case "access_denied":
		a.forget(in.ID)
		writeJSON(w, map[string]any{"status": "denied"})
		return
	default:
		a.forget(in.ID)
		authError(w, http.StatusBadGateway, "GitHub refused the sign-in")
		return
	}
	login, err := a.loginOf(res.AccessToken) // the token ends here: it is never stored
	res.AccessToken = ""
	a.forget(in.ID)
	if err != nil {
		log.Printf("sign-in: the login could not be read")
		authError(w, http.StatusBadGateway, "GitHub did not name the account")
		return
	}
	id := a.openSession(login)
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: id, Path: "/", MaxAge: int(sessionTTL / time.Second),
		HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: secureRequest(r)})
	d.recordSignIn(r, login)
	writeJSON(w, map[string]any{"status": "ok", "login": login})
}

// slowDown answers a pending poll with the interval to wait. A slow_down from GitHub names the
// new interval (or, when it names none, adds five seconds); authorization_pending keeps it.
func (a *authState) slowDown(id string, ghInterval int, slow bool) time.Duration {
	a.mu.Lock()
	defer a.mu.Unlock()
	p := a.pending[id]
	if p == nil {
		return defaultPollGap
	}
	switch {
	case ghInterval > 0:
		p.interval = time.Duration(ghInterval) * time.Second
	case slow:
		p.interval += slowDownStep
	}
	p.nextPoll = a.now().Add(p.interval)
	return p.interval
}

func (a *authState) forget(id string) {
	a.mu.Lock()
	delete(a.pending, id)
	a.mu.Unlock()
}

// loginOf reads the login the token belongs to. It is the only use of the visitor's token.
func (a *authState) loginOf(token string) (string, error) {
	if token == "" {
		return "", errors.New("no token")
	}
	req, err := http.NewRequest(http.MethodGet, a.apiURL+"/user", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := a.client.Do(req)
	if err != nil {
		return "", errors.New("github unreachable")
	}
	defer resp.Body.Close()
	var u struct {
		Login string `json:"login"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, githubBodyCap)).Decode(&u) != nil || !loginRE.MatchString(u.Login) {
		return "", errors.New("no login")
	}
	return u.Login, nil
}

// logout ends the session: POST /auth/logout.
func (d *dash) logout(w http.ResponseWriter, r *http.Request) {
	if d.auth == nil {
		http.NotFound(w, r)
		return
	}
	if !d.authPost(w, r, "logout", 30, nil) {
		return
	}
	if c, err := r.Cookie(sessionCookie); err == nil {
		d.auth.mu.Lock()
		delete(d.auth.sessions, c.Value)
		d.auth.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: secureRequest(r)})
	writeJSON(w, map[string]string{"status": "ok"})
}

// sessionInfo tells the page who it is: GET /api/session. It is 404 when sign-in is off, so
// the page treats the dashboard as it always was.
func (d *dash) sessionInfo(w http.ResponseWriter, r *http.Request) {
	if d.auth == nil {
		http.NotFound(w, r)
		return
	}
	out := map[string]any{"enabled": true, "role": "anonymous", "login": "", "token_missing": d.auth.token == ""}
	if ownerRequest(r) {
		out["role"] = "owner"
	} else if l := d.auth.sessionLogin(r); l != "" && d.trustedHost(r.Host) {
		out["role"], out["login"] = "visitor", l
	}
	writeJSON(w, out)
}
