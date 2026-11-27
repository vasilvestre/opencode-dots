package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"

	"opencode-dots/internal/store"
)

// workerView is one registered worker for the Workers card (ADR 0042). Presence only: the
// instance sends it nothing.
type workerView struct {
	Name         string          `json:"name"`
	Machine      string          `json:"machine"`
	Capabilities json.RawMessage `json:"capabilities"`
	RegisteredAt string          `json:"registered_at"`
	RenewedAt    string          `json:"renewed_at"`
	ExpiresAt    string          `json:"expires_at"`
	Live         bool            `json:"live"`
}

// workers lists the workers, live first, then the expired ones: GET /api/workers. It is read
// only and answers an empty list when there is no database.
func (d *dash) workers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || !d.trustedHost(r.Host) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	out := []workerView{}
	path := filepath.Join(d.state, "dots.db")
	if _, err := os.Stat(path); err != nil {
		writeJSON(w, out)
		return
	}
	st, err := store.Open(path)
	if err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]string{"error": "cannot open the database"})
		return
	}
	defer st.Close()
	list, err := st.Workers(r.Context())
	if err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]string{"error": "cannot read the workers"})
		return
	}
	now := st.Now()
	for _, x := range list {
		out = append(out, workerView{
			Name: x.Name, Machine: x.Machine, Capabilities: x.Capabilities,
			RegisteredAt: x.RegisteredAtUTC(),
			RenewedAt:    x.RenewedAtUTC(),
			ExpiresAt:    x.ExpiresAtUTC(),
			Live:         x.Live(now),
		})
	}
	writeJSON(w, out)
}
