package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"opencode-dots/internal/workertokens"
)

// The HTTP side of the worker tokens (ADR 0042, internal/workertokens): the owner issues and
// revokes them, a worker trades one for a hub token.

// workerTokensAPI is the owner's side: GET lists, POST issues. The secret is in the answer of the
// POST and nowhere else.
func (d *dash) workerTokensAPI(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, map[string]any{"tokens": d.wtokens.List()})
	case http.MethodPost:
		var in struct {
			Label string   `json:"label"`
			Login string   `json:"login"`
			Repos []string `json:"repos"`
			Days  int      `json:"days"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
			return
		}
		known := map[string]bool{}
		for _, k := range d.knownRepos() {
			known[strings.ToLower(k)] = true
		}
		repos := make([]string, 0, len(in.Repos))
		for _, repo := range in.Repos {
			if repo = strings.ToLower(strings.TrimSpace(repo)); !known[repo] {
				writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "unknown repository " + repo})
				return
			}
			repos = append(repos, repo)
		}
		secret, t, err := d.wtokens.Issue(in.Label, in.Login, repos, time.Duration(in.Days)*24*time.Hour)
		if err != nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, map[string]any{"token": secret, "id": t.ID, "expires_at": t.ExpiresAt(), "repos": t.Repos})
	default:
		http.Error(w, "GET or POST", http.StatusMethodNotAllowed)
	}
}

// workerTokenRevoke ends a token at once: the next exchange fails, and the hub drops the worker
// within the 15 minutes of the token it holds.
func (d *dash) workerTokenRevoke(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var in struct {
		ID string `json:"id"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&in) != nil || in.ID == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "an id is required"})
		return
	}
	if !d.wtokens.Revoke(in.ID) {
		writeJSONStatus(w, http.StatusNotFound, map[string]string{"error": "no live token with that id"})
		return
	}
	writeJSON(w, map[string]string{"status": "revoked"})
}

// bearer is the token in an Authorization header, or "".
func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

// hubTokenForWorker trades a worker token for a hub token. It is the only thing the worker
// listener serves, so nothing else of the dashboard is reachable with it.
func (d *dash) hubTokenForWorker(w http.ResponseWriter, r *http.Request) {
	t, ok := d.wtokens.Lookup(bearer(r))
	if !ok {
		time.Sleep(d.wtokens.FailDelay)
		writeJSONStatus(w, http.StatusUnauthorized, map[string]string{"error": "invalid or revoked worker token"})
		return
	}
	repos := d.workerRepos(t)
	sub := t.Login
	if sub == "" {
		sub = "worker:" + t.Label
	}
	d.answerHubToken(w, sub, repos)
}

// workerRepos is the repositories a worker token may serve: the ones it lists, or every one the dot knows.
func (d *dash) workerRepos(t workertokens.Token) []string { return t.Allowed(d.knownRepos()) }

// workerHandler is the mux of the worker listener: the exchange and nothing else.
func (d *dash) workerHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/hub-token", d.hubTokenForWorker)
	mux.HandleFunc("GET /api/hints", d.jobHints)
	mux.HandleFunc("GET /api/jobs", d.jobsPending)
	mux.HandleFunc("POST /api/jobs/{id}/claim", d.jobClaim)
	mux.HandleFunc("POST /api/jobs/{id}/heartbeat", d.jobHeartbeat)
	mux.HandleFunc("POST /api/jobs/{id}/finish", d.jobFinish)
	return mux
}
