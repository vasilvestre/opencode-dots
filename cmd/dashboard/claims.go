package main

import (
	"net/http"
	"os"
	"path/filepath"

	"opencode-dots/internal/store"
)

// claimView is one live claim for the dashboard: what the dot stays away from.
type claimView struct {
	ID        string `json:"id"`
	Repo      string `json:"repo"`
	Title     string `json:"title"`
	WorkItem  string `json:"work_item"`
	Branch    string `json:"branch"`
	Machine   string `json:"machine"`
	ExpiresAt string `json:"expires_at"`
	HeldSince string `json:"held_since"`
}

// claims lists the claims that still hold (ADR 0026). A dashboard with no
// control-plane database answers an empty list: the claim is a veto, and its
// absence is the normal state.
func (d *dash) claims(w http.ResponseWriter, r *http.Request) {
	out := []claimView{}
	path := filepath.Join(d.state, "dots.db")
	if _, err := os.Stat(path); err != nil {
		writeJSON(w, out)
		return
	}
	st, err := store.Open(path)
	if err != nil {
		writeJSON(w, out)
		return
	}
	defer st.Close()
	live, err := st.LiveClaims(r.Context())
	if err != nil {
		writeJSON(w, out)
		return
	}
	for _, c := range live {
		out = append(out, claimView{
			ID: c.ID, Repo: c.Repo, Title: c.Title, WorkItem: c.WorkItemID,
			Branch: c.Branch, Machine: c.Machine, ExpiresAt: c.Expires(),
			HeldSince: c.HeldSince(),
		})
	}
	writeJSON(w, out)
}
