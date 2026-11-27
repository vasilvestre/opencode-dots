// Package auth is the dashboard's GitHub sign-in for visitors (ADR 0044 and 0046,
// docs/dashboard-auth.md): the OAuth device flow, the server-side sessions, what each visitor may
// see (rights.go), the sessions file (sessionstore.go) and a per-client rate limit. It knows
// nothing of HTTP routing: cmd/dashboard decodes requests, calls this and writes answers.
//
// The visitor signs in with the scope `repo`. Right after the approval the token is used once to
// read the login and the permission on each configured repository, then dropped. A session
// keeps the login, the set of visible repositories and an expiry of one hour. It never keeps a
// token.
package auth

import (
	"cmp"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// CookieName is the session cookie; TTL is how long a session lives.
	CookieName = "dots_session"
	TTL        = time.Hour

	maxSessions    = 1000
	maxPending     = 200
	defaultPollGap = 5 * time.Second
	slowDownStep   = 5 * time.Second
	deviceScope    = "repo"
	deviceGrant    = "urn:ietf:params:oauth:grant-type:device_code"
	githubBodyCap  = 1 << 20
)

// loginRE is what a GitHub login looks like, checked before it reaches a URL path.
var loginRE = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

var repoNameRE = regexp.MustCompile(`^[A-Za-z0-9_.-]*[A-Za-z0-9_-][A-Za-z0-9_.-]*/[A-Za-z0-9_.-]*[A-Za-z0-9_-][A-Za-z0-9_.-]*$`)

// The errors a sign-in step can end with; the caller maps them to a status.
var (
	ErrBusy    = errors.New("too many sign-ins in progress")
	ErrUnknown = errors.New("no such sign-in")
	ErrGitHub  = errors.New("GitHub did not answer")
	ErrRefused = errors.New("GitHub refused the sign-in")
	ErrNoLogin = errors.New("GitHub did not name the account")
)

// Config is what the flags and the environment give.
type Config struct {
	ClientID      string
	GitHubURL     string           // device flow host, default https://github.com
	APIURL        string           // REST API, default https://api.github.com
	MinPermission string           // "push" (default) or "pull"
	StateDir      string           // where sessions.json lives; empty keeps sessions in memory only
	Now           func() time.Time // default time.Now; tests inject a clock
}

// Auth is the sign-in state. Build it with New; a nil *Auth means sign-in is off.
type Auth struct {
	clientID  string
	githubURL string
	apiURL    string
	minRank   int
	client    *http.Client
	now       func() time.Time

	mu        sync.Mutex
	pending   map[string]*devicePending
	sessions  map[string]Session // keyed by sessionKey(cookie id), never by the id
	stateDir  string
	fileMu    sync.Mutex // one writer of sessions.json at a time
	saveTimer *time.Timer
	lastWarn  time.Time
	limits    map[string]*window
	readSeen  map[string]time.Time
}

// devicePending is one device flow in progress. The device code never leaves the server.
type devicePending struct {
	deviceCode string
	interval   time.Duration
	nextPoll   time.Time
	expires    time.Time
}

// Session holds a login and the repositories it may see (lower-cased). It never holds a token.
type Session struct {
	Login   string
	Visible map[string]bool
	Expires time.Time
	Created time.Time
}

type window struct {
	start time.Time
	n     int
}

// New builds the sign-in, or nil when no client id is set. The minimum permission must have been
// checked with ParseMinPermission; an empty one is the default.
func New(c Config) *Auth {
	if c.ClientID == "" {
		return nil
	}
	minRank, _ := ParseMinPermission(c.MinPermission)
	a := &Auth{
		clientID:  c.ClientID,
		githubURL: strings.TrimRight(cmp.Or(c.GitHubURL, "https://github.com"), "/"),
		apiURL:    strings.TrimRight(cmp.Or(c.APIURL, "https://api.github.com"), "/"),
		minRank:   minRank,
		client:    &http.Client{Timeout: 10 * time.Second},
		now:       time.Now,
		pending:   map[string]*devicePending{},
		sessions:  map[string]Session{},
		limits:    map[string]*window{},
		readSeen:  map[string]time.Time{},
		stateDir:  c.StateDir,
	}
	if c.Now != nil {
		a.now = c.Now
	}
	a.loadSessions()
	return a
}

// MinPermission is the setting as the sign-in page shows it.
func (a *Auth) MinPermission() string { return PermissionName(a.minRank) }

// ValidLogin says whether s looks like a GitHub login.
func ValidLogin(s string) bool { return loginRE.MatchString(s) }

func randomID(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("no randomness: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// SessionView is the login and the visible repositories of the cookie id, or "" when there is
// none or it expired.
func (a *Auth) SessionView(id string) (string, map[string]bool) {
	if id == "" {
		return "", nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	key, s, ok := a.findSession(id)
	if !ok {
		return "", nil
	}
	if !a.now().Before(s.Expires) {
		delete(a.sessions, key)
		a.scheduleSave()
		return "", nil
	}
	return s.Login, s.Visible
}

// OpenSession starts a session and returns the cookie id.
func (a *Auth) OpenSession(login string, visible map[string]bool) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pruneSessions()
	now := a.now()
	id := randomID(32)
	a.sessions[sessionKey(id)] = Session{Login: login, Visible: visible, Expires: now.Add(TTL), Created: now}
	a.capSessions() // drops the oldest beyond the cap
	a.scheduleSave()
	return id
}

// CloseSession forgets the session of a cookie id.
func (a *Auth) CloseSession(id string) {
	a.mu.Lock()
	delete(a.sessions, sessionKey(id))
	a.scheduleSave()
	a.mu.Unlock()
}

// Allow is a fixed window per client key and kind: n requests a minute.
func (a *Auth) Allow(client, kind string, n int) bool {
	key := kind + "|" + client
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

// SeenRecently says whether key was seen less than every ago, and records it when it was not.
func (a *Auth) SeenRecently(key string, every time.Duration) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	if at, ok := a.readSeen[key]; ok && now.Sub(at) < every {
		return true
	}
	if len(a.readSeen) > 5000 {
		for k, at := range a.readSeen {
			if now.Sub(at) >= every {
				delete(a.readSeen, k)
			}
		}
	}
	a.readSeen[key] = now
	return false
}

// githubForm posts a form to the device flow host and decodes the JSON answer.
func (a *Auth) githubForm(path string, form url.Values, out any) error {
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

// Device is what the page shows a visitor who starts a sign-in.
type Device struct {
	ID        string `json:"id"`
	UserCode  string `json:"user_code"`
	URI       string `json:"verification_uri"`
	Interval  int    `json:"interval"`
	ExpiresIn int    `json:"expires_in"`
}

// StartDevice begins a device flow. It asks for the scope `repo`, the one `gh auth login` asks,
// so GitHub can say what the visitor may do on each repository.
func (a *Auth) StartDevice() (Device, error) {
	var out struct {
		DeviceCode string `json:"device_code"`
		UserCode   string `json:"user_code"`
		URI        string `json:"verification_uri"`
		ExpiresIn  int    `json:"expires_in"`
		Interval   int    `json:"interval"`
	}
	if err := a.githubForm("/login/device/code", url.Values{"client_id": {a.clientID}, "scope": {deviceScope}}, &out); err != nil ||
		out.DeviceCode == "" || out.UserCode == "" || out.ExpiresIn <= 0 {
		log.Printf("sign-in: the device code request failed")
		return Device{}, ErrGitHub
	}
	interval := time.Duration(out.Interval) * time.Second
	if interval < time.Second {
		interval = defaultPollGap
	}
	now := a.now()
	id := randomID(16)
	a.mu.Lock()
	defer a.mu.Unlock()
	for k, p := range a.pending {
		if !now.Before(p.expires) {
			delete(a.pending, k)
		}
	}
	if len(a.pending) >= maxPending {
		return Device{}, ErrBusy
	}
	a.pending[id] = &devicePending{deviceCode: out.DeviceCode, interval: interval, nextPoll: now.Add(interval),
		expires: now.Add(time.Duration(out.ExpiresIn) * time.Second)}
	return Device{ID: id, UserCode: out.UserCode, URI: out.URI, Interval: int(interval / time.Second), ExpiresIn: out.ExpiresIn}, nil
}

// Poll is the answer to a poll: Status is pending, expired, denied or ok. Only ok carries a
// session (the cookie id) and the login. The token is never in it.
type Poll struct {
	Status    string
	Interval  int // seconds to wait, with pending
	Login     string
	SessionID string
}

// PollDevice asks GitHub whether the visitor approved. repos lists the repositories whose
// permission is read when the sign-in completes.
func (a *Auth) PollDevice(id string, repos func() []string) (Poll, error) {
	a.mu.Lock()
	p := a.pending[id]
	now := a.now()
	if p == nil {
		a.mu.Unlock()
		return Poll{}, ErrUnknown
	}
	if !now.Before(p.expires) {
		delete(a.pending, id)
		a.mu.Unlock()
		return Poll{Status: "expired"}, nil
	}
	if now.Before(p.nextPoll) { // GitHub is not asked before the interval has passed
		interval := int(p.interval / time.Second)
		a.mu.Unlock()
		return Poll{Status: "pending", Interval: interval}, nil
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
		return Poll{}, ErrGitHub
	}
	switch res.Error {
	case "":
	case "authorization_pending":
		return Poll{Status: "pending", Interval: int(a.slowDown(id, 0, false) / time.Second)}, nil
	case "slow_down":
		return Poll{Status: "pending", Interval: int(a.slowDown(id, res.Interval, true) / time.Second)}, nil
	case "expired_token":
		a.forget(id)
		return Poll{Status: "expired"}, nil
	case "access_denied":
		a.forget(id)
		return Poll{Status: "denied"}, nil
	default:
		a.forget(id)
		return Poll{}, ErrRefused
	}
	// The token is used here and nowhere else: it is never stored, logged or sent on.
	login, err := a.loginOf(res.AccessToken)
	var visible map[string]bool
	if err == nil {
		visible = a.VisibleRepos(res.AccessToken, repos())
	}
	res.AccessToken = ""
	a.forget(id)
	if err != nil {
		log.Printf("sign-in: the login could not be read")
		return Poll{}, ErrNoLogin
	}
	return Poll{Status: "ok", Login: login, SessionID: a.OpenSession(login, visible)}, nil
}

// slowDown answers a pending poll with the interval to wait. A slow_down from GitHub names the
// new interval (or, when it names none, adds five seconds); authorization_pending keeps it.
func (a *Auth) slowDown(id string, ghInterval int, slow bool) time.Duration {
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

func (a *Auth) forget(id string) {
	a.mu.Lock()
	delete(a.pending, id)
	a.mu.Unlock()
}

// loginOf reads the login the token belongs to.
func (a *Auth) loginOf(token string) (string, error) {
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
