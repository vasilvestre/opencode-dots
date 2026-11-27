package main

import (
	"bufio"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"opencode-dots/internal/events"
)

// hubTokenTTL is how long a subscriber token lives. A client asks again before it ends, so a
// session that is gone stops following within this time.
const hubTokenTTL = 15 * time.Minute

// hubIssuer hands a signed-in person a token for the Mercure hub: only the topics of the
// repositories they may reach (ADR 0046), nothing else. The hub key never leaves this process.
type hubIssuer struct {
	url      string // the hub endpoint a client follows, such as https://hub.example/.well-known/mercure
	audience string // the endpoint as the hub sees a request: a proxy that ends TLS makes it http
	hub      events.Hub
}

// newHubIssuer reads the hub's own settings file (MERCURE_SUBSCRIBER_JWT_KEY and
// MERCURE_TRUSTED_ISSUERS, written by deploy/types/local/hub.sh). With no URL it is off.
func newHubIssuer(url, audience, envFile string) (*hubIssuer, error) {
	if url == "" {
		return nil, nil
	}
	env, err := readEnvFile(envFile)
	if err != nil {
		return nil, fmt.Errorf("hub settings: %w", err)
	}
	key := env["MERCURE_SUBSCRIBER_JWT_KEY"]
	if key == "" {
		return nil, fmt.Errorf("hub settings: %s has no MERCURE_SUBSCRIBER_JWT_KEY", envFile)
	}
	if audience == "" {
		audience = url
	}
	issuer := env["MERCURE_TRUSTED_ISSUERS"]
	if i := strings.IndexByte(issuer, ' '); i >= 0 {
		issuer = issuer[:i] // the first trusted issuer is the one tokens carry
	}
	return &hubIssuer{url: url, audience: audience, hub: events.Hub{URL: url, Secret: []byte(key), Issuer: issuer}}, nil
}

// readEnvFile parses KEY=VALUE lines: comments and blanks are skipped, values are not expanded.
func readEnvFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			out[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return out, sc.Err()
}

// hubToken answers a signed-in person (or the owner, who reaches every repository) with the hub
// endpoint and a short token for the topics of their repositories.
func (d *dash) hubToken(w http.ResponseWriter, r *http.Request) {
	sub := "owner"
	var repos []string
	switch {
	case d.auth == nil || ownerRequest(r):
		repos = d.knownRepos()
	default:
		login, visible := d.auth.sessionView(r)
		if login == "" || !d.trustedHost(r.Host) {
			writeJSONStatus(w, http.StatusUnauthorized, map[string]string{"error": "sign in first"})
			return
		}
		sub = login
		for _, repo := range d.knownRepos() {
			if visible[strings.ToLower(repo)] {
				repos = append(repos, repo)
			}
		}
	}
	if d.hub == nil {
		http.NotFound(w, r) // after the session check: an anonymous caller learns nothing about the setup
		return
	}
	topics := make([]string, 0, len(repos))
	for _, repo := range repos {
		topics = append(topics, events.RepoTopic(repo))
	}
	tok := d.hub.hub.SubscriberToken(sub, d.hub.audience, topics, hubTokenTTL)
	writeJSON(w, map[string]any{
		"hub":        d.hub.url,
		"token":      tok,
		"topics":     topics,
		"expires_at": time.Now().Add(hubTokenTTL).UTC().Format(time.RFC3339),
	})
}
