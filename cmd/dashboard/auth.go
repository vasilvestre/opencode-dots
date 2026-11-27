package main

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"

	"opencode-dots/internal/auth"
)

// The HTTP side of GitHub sign-in for visitors (ADR 0044, docs/dashboard-auth.md). The device
// flow, the sessions and the rate limit live in internal/auth; this file decodes requests, runs
// the checks every sign-in endpoint shares, and writes the answers. It is off unless a GitHub
// OAuth client id is configured: d.auth is nil, nothing here runs and the dashboard answers as it
// always did.

const authBodyCap = 4096

// ownerRequest is the host's own user: the request reached the dashboard directly on
// loopback, with no tailnet login header. Anything `tailscale serve` forwards is a visitor.
func ownerRequest(r *http.Request) bool {
	if teamRequest(r) {
		return false // the team listener serves visitors only (team.go)
	}
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

// sessionView is the login and the visible repositories of the request's session, or "" when
// there is none. The caller has checked d.auth is not nil.
func (d *dash) sessionView(r *http.Request) (string, map[string]bool) {
	c, err := r.Cookie(auth.CookieName)
	if err != nil {
		return "", nil
	}
	return d.auth.SessionView(c.Value)
}

func (d *dash) sessionLogin(r *http.Request) string {
	login, _ := d.sessionView(r)
	return login
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
	if !d.auth.Allow(clientKey(r), kind, perMinute) {
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

func newSessionCookie(r *http.Request, id string, maxAge int) *http.Cookie {
	return &http.Cookie{Name: auth.CookieName, Value: id, Path: "/", MaxAge: maxAge,
		HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: secureRequest(r)}
}

// deviceStart begins a device flow: POST /auth/device/start.
func (d *dash) deviceStart(w http.ResponseWriter, r *http.Request) {
	if d.auth == nil {
		http.NotFound(w, r)
		return
	}
	var in struct{}
	if !d.authPost(w, r, "start", 10, &in) {
		return
	}
	dev, err := d.auth.StartDevice()
	switch {
	case errors.Is(err, auth.ErrBusy):
		authError(w, http.StatusTooManyRequests, err.Error())
	case err != nil:
		authError(w, http.StatusBadGateway, "GitHub did not start the sign-in")
	default:
		writeJSON(w, dev)
	}
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
	p, err := d.auth.PollDevice(in.ID, d.knownRepos)
	switch {
	case errors.Is(err, auth.ErrUnknown):
		authError(w, http.StatusNotFound, err.Error())
		return
	case errors.Is(err, auth.ErrRefused), errors.Is(err, auth.ErrNoLogin), errors.Is(err, auth.ErrGitHub):
		authError(w, http.StatusBadGateway, err.Error())
		return
	}
	switch p.Status {
	case "pending":
		writeJSON(w, map[string]any{"status": "pending", "interval": p.Interval})
	case "ok":
		http.SetCookie(w, newSessionCookie(r, p.SessionID, int(auth.TTL.Seconds())))
		d.recordSignIn(r, p.Login)
		writeJSON(w, map[string]any{"status": "ok", "login": p.Login})
	default:
		writeJSON(w, map[string]any{"status": p.Status})
	}
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
	if c, err := r.Cookie(auth.CookieName); err == nil {
		d.auth.CloseSession(c.Value)
	}
	http.SetCookie(w, newSessionCookie(r, "", -1))
	writeJSON(w, map[string]string{"status": "ok"})
}

// sessionInfo tells the page who it is: GET /api/session. It is 404 when sign-in is off, so
// the page treats the dashboard as it always was.
func (d *dash) sessionInfo(w http.ResponseWriter, r *http.Request) {
	if d.auth == nil {
		http.NotFound(w, r)
		return
	}
	out := map[string]any{"enabled": true, "role": "anonymous", "login": "", "min_permission": d.auth.MinPermission()}
	if ownerRequest(r) {
		out["role"] = "owner"
	} else if l := d.sessionLogin(r); l != "" && d.trustedHost(r.Host) {
		out["role"], out["login"] = "visitor", l
	}
	writeJSON(w, out)
}
