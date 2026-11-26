package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// A click on Release files an approval: it starts the publish-held workflow
// through awf serve, which publishes. The dashboard holds no GitHub token and
// never publishes itself (ADR 0040, docs/publish-held.md).
//
// awf's run request takes inputs only, so RELEASED_BY cannot be set from here:
// the workflow then names the account behind its own token in the log.

var releaseMu sync.Mutex

// releasedItems lists held/released/, newest first.
func (d *dash) releasedItems() []gateItem {
	out := []gateItem{}
	dir := filepath.Join(d.state, "held", "released")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	var names []string
	for _, e := range entries {
		if autonomyNameRE.MatchString(e.Name()) {
			names = append(names, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	if len(names) > autonomyListCap {
		names = names[:autonomyListCap]
	}
	for _, n := range names {
		text, ok := readCapped(filepath.Join(dir, n), autonomyNoteCap)
		if !ok {
			continue
		}
		if it, ok := parseGateFile(n, "released", text); ok {
			out = append(out, it)
		}
	}
	return out
}

// autonomyRelease starts publish-held for one held record.
func (d *dash) autonomyRelease(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST only"})
		return
	}
	if !d.trustedRequest(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]string{"error": "loopback only"})
		return
	}
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		writeJSONStatus(w, http.StatusUnsupportedMediaType, map[string]string{"error": "JSON only"})
		return
	}
	var in struct {
		ID string `json:"id"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil || !autonomyNameRE.MatchString(in.ID) {
		writeJSONStatus(w, http.StatusUnprocessableEntity, map[string]string{"error": "id must be the file name of a held record"})
		return
	}
	releaseMu.Lock()
	defer releaseMu.Unlock()
	if _, err := os.Lstat(filepath.Join(d.state, "held", "released", in.ID)); err == nil {
		writeJSONStatus(w, http.StatusConflict, map[string]string{"error": "already released"})
		return
	}
	if info, err := os.Lstat(filepath.Join(d.state, "held", in.ID)); err != nil || !info.Mode().IsRegular() {
		writeJSONStatus(w, http.StatusNotFound, map[string]string{"error": "no such held record"})
		return
	}
	id, err := d.startRun("publish-held", map[string]string{"id": in.ID})
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, map[string]string{"execution_id": id})
}

// health serves what bin/health --json last wrote. Read only.
func (d *dash) health(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || !d.trustedHost(r.Host) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	p := filepath.Join(d.state, "health.json")
	info, err := os.Lstat(p)
	text, ok := readCapped(p, 256<<10)
	var data struct {
		Overall string            `json:"overall"`
		Checks  []json.RawMessage `json:"checks"`
	}
	if err != nil || !ok || json.Unmarshal([]byte(text), &data) != nil || data.Overall == "" {
		writeJSON(w, map[string]any{"present": false})
		return
	}
	if data.Checks == nil {
		data.Checks = []json.RawMessage{}
	}
	writeJSON(w, map[string]any{
		"present": true,
		"overall": data.Overall,
		"checks":  data.Checks,
		"mtime":   info.ModTime().UTC().Format("2006-01-02T15:04:05Z"),
	})
}
