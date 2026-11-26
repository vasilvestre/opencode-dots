package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
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
	inputs := map[string]string{"id": in.ID}
	if text, ok := readCapped(filepath.Join(d.state, "held", in.ID), autonomyNoteCap); ok {
		if it, ok := parseGateFile(in.ID, "held", text); ok && it.Action == "file-issue" {
			// The workflow repeats the checks of file-upstream, which needs the
			// repository the record came from and that repository's upstream list.
			repo, list := d.upstreamOf(text, it.Target)
			if repo == "" || list == "" {
				writeJSONStatus(w, http.StatusUnprocessableEntity, map[string]string{"error": "cannot tell which repository this file-issue record belongs to, or its upstream list is not in awf-gh.yaml; run publish-held with repo and upstream"})
				return
			}
			inputs["repo"], inputs["upstream"] = repo, list
		}
	}
	id, err := d.startRun("publish-held", inputs)
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, map[string]string{"execution_id": id})
}

// fileUpstreamSourceRE reads the source repository out of the path the gate
// recorded for a file-issue body: STATE/upstream/_work/OWNER/REPO/issue.md.
var fileUpstreamSourceRE = regexp.MustCompile(`/upstream/_work/([A-Za-z0-9._-]+/[A-Za-z0-9._-]+)/issue\.md$`)

// upstreamOf finds the repository a file-issue record came from and its
// `upstream` input (owner/name=cap joined by commas) in the watcher config. The
// record's `source` line names the repository; without it, the one repository
// whose list holds the target. Empty strings when it cannot tell.
func (d *dash) upstreamOf(text, target string) (string, string) {
	var cfg struct {
		Repos []struct {
			Repo   string            `yaml:"repo"`
			Inputs map[string]string `yaml:"inputs"`
		} `yaml:"repos"`
	}
	if yaml.Unmarshal([]byte(readFile(d.awfgh, 1<<20)), &cfg) != nil {
		return "", ""
	}
	lists := map[string]string{}
	for _, e := range cfg.Repos {
		if l := e.Inputs["upstream"]; l != "" {
			lists[e.Repo] = l
		}
	}
	for _, line := range strings.Split(text, "\n") {
		if v, ok := strings.CutPrefix(line, "source: "); ok {
			if m := fileUpstreamSourceRE.FindStringSubmatch(strings.TrimSpace(v)); m != nil {
				return m[1], lists[m[1]]
			}
			break
		}
	}
	found, list := "", ""
	for repo, l := range lists {
		for _, e := range strings.Split(l, ",") {
			if strings.EqualFold(strings.SplitN(e, "=", 2)[0], target) {
				if found != "" {
					return "", ""
				}
				found, list = repo, l
			}
		}
	}
	return found, list
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
