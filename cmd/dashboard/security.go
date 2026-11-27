package main

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

// trustedRequest accepts a request whose Host and Origin are this machine or an -allow-host name.
// An absent Origin is fine: curl and Fetch from the page send one, but a
// same-origin navigation may not.
func (d *dash) trustedRequest(r *http.Request) bool {
	if !d.trustedHost(r.Host) {
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		if err != nil || !d.trustedHost(u.Host) {
			return false
		}
	}
	return true
}

// jsonWrite runs the checks every JSON write shares: POST, a trusted Host and Origin, and a JSON
// content type (a cross-site form cannot send it without a preflight). It has answered when it
// returns false.
func (d *dash) jsonWrite(w http.ResponseWriter, r *http.Request) bool {
	switch {
	case r.Method != http.MethodPost:
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST only"})
	case !d.trustedRequest(r):
		writeJSONStatus(w, http.StatusForbidden, map[string]string{"error": "loopback or an allowed host only"})
	case !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json"):
		writeJSONStatus(w, http.StatusUnsupportedMediaType, map[string]string{"error": "JSON only"})
	default:
		return true
	}
	return false
}

// trustedHost admits loopback and the hosts named by -allow-host. The tailnet
// proxy forwards the tailnet name, so a page served through it writes only when
// that name is on the list.
func (d *dash) trustedHost(hostport string) bool {
	if isLoopbackHost(hostport) {
		return true
	}
	_, ok := d.allow[hostOnly(hostport)]
	return ok
}

func isLoopbackHost(hostport string) bool {
	host := hostOnly(hostport)
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func hostOnly(hostport string) string {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	return strings.Trim(host, "[]")
}
