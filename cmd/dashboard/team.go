package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
)

// The team listener (docs/team-ui.md). A developer reaches the dashboard through an SSH local
// forward, and that tunnel ends on the server's loopback, exactly where the owner's own request
// does. So a second listener serves the same routes, and every request that arrives on it is a
// visitor, whatever its source address, Host or headers. The identity is the listener, carried in
// the request context by teamHandler; it is never inferred from anything the client sends.

type teamKey struct{}

// teamRequest is true for a request that arrived on the team listener.
func teamRequest(r *http.Request) bool {
	v, _ := r.Context().Value(teamKey{}).(bool)
	return v
}

// teamHandler marks every request as a team one and applies the checks of this listener: the
// Host must be a loopback one (any port: the developer picks the local port of the forward), and
// a request that carries an Origin must be same-origin.
func teamHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isLoopbackHost(r.Host) {
			authError(w, http.StatusForbidden, "forbidden")
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			u, err := url.Parse(origin)
			if err != nil || u.Host != r.Host {
				authError(w, http.StatusForbidden, "forbidden")
				return
			}
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), teamKey{}, true)))
	})
}

// checkTeamAddr refuses a team address the dashboard must not serve: not a loopback IP literal
// with a port, sign-in off, or the owner listener's own address. A host name is refused, since
// it could resolve elsewhere.
func checkTeamAddr(team, owner string, signIn bool) error {
	if team == "" {
		return nil
	}
	host, port, err := net.SplitHostPort(team)
	if err != nil || port == "" {
		return fmt.Errorf("-team-addr %q: want an IP address and a port, for example 127.0.0.1:8098", team)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("-team-addr %q: the team listener binds a loopback IP address only (127.0.0.0/8 or ::1)", team)
	}
	if !signIn {
		return errors.New("-team-addr needs sign-in: set -github-client-id (DOTS_GITHUB_CLIENT_ID)")
	}
	if sameAddr(team, owner) {
		return fmt.Errorf("-team-addr %q is the owner listener's address: use another port", team)
	}
	return nil
}

func sameAddr(a, b string) bool {
	ah, ap, err1 := net.SplitHostPort(a)
	bh, bp, err2 := net.SplitHostPort(b)
	if err1 != nil || err2 != nil || ap != bp {
		return a == b
	}
	ai, bi := net.ParseIP(ah), net.ParseIP(bh)
	if ai == nil || bi == nil {
		return ah == bh
	}
	return ai.Equal(bi)
}

// checkWorkerAddr refuses a worker listener address that is not a loopback IP literal with a
// port, or that is another listener's own.
func checkWorkerAddr(worker, owner, team string) error {
	host, port, err := net.SplitHostPort(worker)
	if err != nil || port == "" {
		return fmt.Errorf("-worker-addr %q: want an IP address and a port, for example 127.0.0.1:8096", worker)
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("-worker-addr %q: the worker listener binds a loopback IP address only", worker)
	}
	if worker == owner || worker == team {
		return fmt.Errorf("-worker-addr %q is already another listener's address", worker)
	}
	return nil
}
