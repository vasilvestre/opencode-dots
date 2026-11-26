package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"opencode-dots/internal/repos"
)

// Executors are the awf serve instances that run the tasks of the repositories
// naming them (ADR 0031). This machine only watches, so its own awf never sees
// those runs. The dashboard lists them from each executor's awf, and keeps the
// last answer on disk so the history stays visible while the executor is off.

const remoteEvery = 5 * time.Second

type remoteSnapshot struct {
	Name       string           `json:"name"`
	Up         bool             `json:"up"`
	Checked    string           `json:"checked,omitempty"`
	Executions []map[string]any `json:"executions"`
	Entries    []map[string]any `json:"entries"`
}

// executorAddrs reads settings.executors of repos.yaml on every call, so an
// edit needs no restart. A missing or invalid file means no executor.
func (d *dash) executorAddrs() map[string]string {
	doc, err := repos.Load(filepath.Join(d.root, "repos.yaml"))
	if err != nil {
		return nil
	}
	return doc.Settings.Executors
}

func (d *dash) snapshotFile(name string) string {
	return filepath.Join(d.state, "executors", name+".json")
}

// pollExecutors refreshes the snapshot of every executor, at most once per
// remoteEvery. A failed poll keeps the previous lists and only marks it down.
func (d *dash) pollExecutors() {
	if time.Since(d.remoteAt) < remoteEvery {
		return
	}
	d.remoteAt = time.Now()
	addrs := d.executorAddrs()
	d.remoteMu.Lock()
	for name := range d.remote {
		if _, ok := addrs[name]; !ok {
			delete(d.remote, name)
		}
	}
	d.remoteMu.Unlock()
	for name, base := range addrs {
		snap := d.loadSnapshot(name)
		snap.Name, snap.Checked = name, time.Now().UTC().Format(time.RFC3339)
		execs, okE := fetchAwfList(base+"/api/executions", "executions")
		hist, okH := fetchAwfList(base+"/api/history?limit=50", "entries")
		snap.Up = okE && okH
		if snap.Up {
			snap.Executions, snap.Entries = execs, hist
			linkRemote(&snap)
			if os.MkdirAll(filepath.Dir(d.snapshotFile(name)), 0o755) == nil {
				if b, err := json.Marshal(snap); err == nil {
					os.WriteFile(d.snapshotFile(name), b, 0o644)
				}
			}
		}
		d.remoteMu.Lock()
		if d.remote == nil {
			d.remote = map[string]remoteSnapshot{}
		}
		d.remote[name] = snap
		d.remoteMu.Unlock()
	}
}

// loadSnapshot returns what the dashboard knows of an executor: memory first,
// else the file a previous process wrote.
func (d *dash) loadSnapshot(name string) remoteSnapshot {
	d.remoteMu.Lock()
	snap, ok := d.remote[name]
	d.remoteMu.Unlock()
	if ok {
		return snap
	}
	var disk remoteSnapshot
	if b, err := os.ReadFile(d.snapshotFile(name)); err == nil {
		json.Unmarshal(b, &disk)
	}
	return disk
}

func fetchAwfList(url, key string) ([]map[string]any, bool) {
	client := http.Client{Timeout: 4 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	var out struct {
		Body map[string][]map[string]any `json:"body"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&out) != nil {
		return nil, false
	}
	return out.Body[key], true
}

// linkRemote gives each history entry the execution id awf lists under the
// same workflow and start, like the page does for the local awf, so a row
// carries one id whatever machine ran it.
func linkRemote(s *remoteSnapshot) {
	byKey := map[string]string{}
	for _, e := range s.Executions {
		byKey[runKey(e["workflow_name"], e["started_at"])] = str(e["execution_id"])
	}
	for _, e := range s.Entries {
		if id := byKey[runKey(e["workflow_name"], e["started_at"])]; id != "" {
			e["execution_id"] = id
		}
	}
}

func runKey(workflow, started any) string {
	s := str(started)
	if len(s) > 19 {
		s = s[:19]
	}
	return str(workflow) + "|" + s
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

// remote serves the executors' runs, tagged with the executor that ran them.
func (d *dash) remoteRuns(w http.ResponseWriter, r *http.Request) {
	d.remoteMu.Lock()
	names := make([]string, 0, len(d.remote))
	for n := range d.remote {
		names = append(names, n)
	}
	sort.Strings(names)
	out := struct {
		Executors  []remoteSnapshot `json:"executors"`
		Executions []map[string]any `json:"executions"`
		Entries    []map[string]any `json:"entries"`
	}{[]remoteSnapshot{}, []map[string]any{}, []map[string]any{}}
	for _, n := range names {
		s := d.remote[n]
		out.Executors = append(out.Executors, remoteSnapshot{Name: s.Name, Up: s.Up, Checked: s.Checked})
		for _, e := range s.Executions {
			out.Executions = append(out.Executions, tagged(e, n, s.Up))
		}
		for _, e := range s.Entries {
			out.Entries = append(out.Entries, tagged(e, n, s.Up))
		}
	}
	d.remoteMu.Unlock()
	writeJSON(w, out)
}

// tagged copies a record and names its executor. A run listed as running by an
// executor that is now down is not running: the answer is stale.
func tagged(e map[string]any, executor string, up bool) map[string]any {
	c := make(map[string]any, len(e)+2)
	for k, v := range e {
		c[k] = v
	}
	c["executor"] = executor
	if !up && strings.EqualFold(str(c["status"]), "running") {
		c["status"] = "unknown"
	}
	return c
}
