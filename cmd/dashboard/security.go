package main

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

// loopbackRequest accepts a request whose Host and Origin are this machine.
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

// trustedHost admits loopback and the hosts named by -allow-host. The NetBird
// proxy forwards the dashboard host, so a page served through it writes only when
// that host is on the list.
func (d *dash) trustedHost(hostport string) bool {
	if isLoopbackHost(hostport) {
		return true
	}
	_, ok := d.allow[hostOnly(hostport)]
	return ok
}

func loopbackRequest(r *http.Request) bool {
	if !isLoopbackHost(r.Host) {
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		if err != nil || !isLoopbackHost(u.Host) {
			return false
		}
	}
	return true
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
