package main

import (
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"slices"
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
			priceRemote(&snap, d.execDash[name])
			if os.MkdirAll(filepath.Dir(d.snapshotFile(name)), 0o755) == nil {
				if b, err := json.Marshal(snap); err == nil {
					_ = os.WriteFile(d.snapshotFile(name), b, 0o644)
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
		_ = json.Unmarshal(b, &disk)
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
	names := slices.Sorted(maps.Keys(d.remote))
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
	maps.Copy(c, e)
	c["executor"] = executor
	if !up && strings.EqualFold(str(c["status"]), "running") {
		c["status"] = "unknown"
	}
	return c
}

// parseExecutorDashboards reads "name=url,name=url". Only http addresses are kept.
func parseExecutorDashboards(spec string) map[string]string {
	out := map[string]string{}
	for part := range strings.SplitSeq(spec, ",") {
		name, addr, ok := strings.Cut(strings.TrimSpace(part), "=")
		if u, err := url.Parse(addr); ok && err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" {
			out[strings.TrimSpace(name)] = strings.TrimRight(addr, "/")
		}
	}
	return out
}

// executorOf names the executor whose awf listed the run, or "".
func (d *dash) executorOf(id string) string {
	d.remoteMu.Lock()
	defer d.remoteMu.Unlock()
	for name, s := range d.remote {
		for _, e := range s.Executions {
			if str(e["execution_id"]) == id {
				return name
			}
		}
	}
	return ""
}

// viaExecutor serves a run's log, stream, steps or cost from the executor's
// own dashboard when the run was done there. The executor's dashboard is the
// one that recorded it: awf gives its events to a single reader, so this
// machine must not read them from awf too. A run with a local log is served
// here, and so is any run whose executor has no dashboard address.
func (d *dash) viaExecutor(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("id")
		if logID(id) && !exists(filepath.Join(d.state, "logs", id+".jsonl")) && d.transcriptPath(id) == "" {
			if base := d.execDash[d.executorOf(id)]; base != "" {
				target, _ := url.Parse(base)
				proxy := httputil.NewSingleHostReverseProxy(target)
				proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
					writeJSONStatus(w, http.StatusBadGateway, map[string]string{"error": "the executor's dashboard is unreachable: " + err.Error()})
				}
				proxy.ServeHTTP(w, r)
				return
			}
		}
		next(w, r)
	}
}

// priceRemote copies what the executor's own dashboard knows of each run (price,
// target, agent CLI) onto the entries, since only that dashboard has the logs.
// Without its address, or while it is unreachable, the entries keep what they had.
func priceRemote(s *remoteSnapshot, dashboard string) {
	if dashboard == "" {
		return
	}
	client := http.Client{Timeout: 4 * time.Second}
	resp, err := client.Get(dashboard + "/api/logs")
	if err != nil {
		return
	}
	defer resp.Body.Close()
	var rows []struct {
		ID       string  `json:"execution_id"`
		Cost     float64 `json:"cost"`
		Repo     string  `json:"repo"`
		PR       string  `json:"pr"`
		Issue    string  `json:"issue"`
		Provider string  `json:"provider"`
		Route    string  `json:"route"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&rows) != nil {
		return
	}
	byID := map[string]int{}
	for i, r := range rows {
		byID[r.ID] = i
	}
	for _, e := range s.Entries {
		i, ok := byID[str(e["execution_id"])]
		if !ok {
			continue
		}
		r := rows[i]
		e["cost"] = r.Cost
		e["provider"], e["route"] = r.Provider, r.Route
		if num := r.PR + r.Issue; r.Repo != "" && num != "" {
			e["target"] = r.Repo + "#" + num
		}
	}
}
