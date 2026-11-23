// Command dashboard serves a loopback page for what opencode-dots does: awf
// runs, PR review and reply artifacts, and the watcher's seen list.
//
// It also proxies /awf/* to the awf API so the page stays same-origin and needs
// no CORS. It binds 127.0.0.1 only. Writes are limited to state/logs/ and the
// four local config files (see /api/save and ADR 0004).
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

type review struct {
	ID        string `json:"id"`
	Repo      string `json:"repo"`
	PR        string `json:"pr"`
	Title     string `json:"title"`
	Author    string `json:"author"`
	Base      string `json:"base"`
	HasReview bool   `json:"has_review"`
	DiffLines int    `json:"diff_lines"`
	State     string `json:"state,omitempty"`
	URL       string `json:"url,omitempty"`
}

type reply struct {
	ID       string `json:"id"`
	Repo     string `json:"repo"`
	PR       string `json:"pr"`
	Title    string `json:"title"`
	URL      string `json:"url"`
	Author   string `json:"author"`
	Branch   string `json:"branch"`
	Comments int    `json:"comments"`
	HasReply bool   `json:"has_reply"`
	Pushed   string `json:"pushed"`
	State    string `json:"state,omitempty"`
}

type seen struct {
	Repo string `json:"repo"`
	PR   string `json:"pr"`
}

type artifacts struct {
	Reviews []review `json:"reviews"`
	Replies []reply  `json:"replies"`
	Seen    []seen   `json:"seen"`
}

type ghEntry struct {
	Repo          string   `json:"repo" yaml:"repo"`
	Scope         string   `json:"scope" yaml:"scope"`
	Workflow      string   `json:"workflow" yaml:"workflow"`
	On            []string `json:"on" yaml:"on"`
	IgnoreAuthors []string `json:"ignore_authors" yaml:"ignore_authors"`
	OnlyAuthors   []string `json:"only_authors" yaml:"only_authors"`
}

// watchedRepo groups the watcher's config entries by repository, so a repo
// watched by two workflows appears once.
type watchedRepo struct {
	Repo    string    `json:"repo"`
	Entries []ghEntry `json:"entries"`
	Seen    []string  `json:"seen"`

	// TowerIgnore is set only when Tower is enabled and the repository has a
	// local checkout whose .towerignore is missing or holds no pattern.
	TowerIgnore string `json:"tower_ignore,omitempty"`
}

// Artifact reads stop at these directories: state/ also holds git clones.
var artifactDirs = map[string]bool{"reviews": true, "replies": true}

var artifactExt = map[string]string{
	".md":    "text/markdown; charset=utf-8",
	".json":  "application/json",
	".patch": "text/plain; charset=utf-8",
	".sha":   "text/plain; charset=utf-8",
}

func main() {
	root := projectRoot()
	addr := flag.String("addr", "127.0.0.1:8099", "listen address")
	state := flag.String("state", envOr("STATE_DIR", filepath.Join(root, "state")), "state directory")
	repoRoot := flag.String("root", root, "repository root holding workflows/ and the local config files")
	awfgh := flag.String("awfgh", filepath.Join(root, "awf-gh.yaml"), "watcher config")
	awf := flag.String("awf", envOr("AWF_URL", "http://127.0.0.1:2511"), "awf API base URL")
	ui := flag.String("ui", filepath.Join(root, "ui", "index.html"), "dashboard page")
	transcripts := flag.String("transcripts", "", "awf transcript directory, empty finds it in the usual places")
	opencode := flag.String("opencode", defaultOpencodeConfig(), "opencode config declaring the tower MCP server")
	flag.Parse()

	target, err := url.Parse(*awf)
	if err != nil {
		log.Fatalf("bad awf URL %q: %v", *awf, err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Printf("awf proxy: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(map[string]string{"error": "awf unreachable at " + *awf + ": " + err.Error()})
	}

	d := &dash{state: *state, root: *repoRoot, awf: *awf, awfgh: *awfgh, transcripts: *transcripts, opencode: *opencode, active: map[string]bool{}, tried: map[string]bool{}}
	d.costs = map[string]costMemo{}
	d.indexed = d.loadIndexed()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			// Everything the page needs lives in ui/: modules, stylesheet, icon.
			if name := strings.TrimPrefix(r.URL.Path, "/"); safeAsset(name) {
				serveFile(w, r, filepath.Join(filepath.Dir(*ui), name), assetType(name))
				return
			}
			http.NotFound(w, r)
			return
		}
		serveFile(w, r, *ui, "text/html; charset=utf-8")
	})
	mux.HandleFunc("/api/artifacts", d.artifacts)
	mux.HandleFunc("/api/file", d.file)
	mux.HandleFunc("/api/log", d.log)
	mux.HandleFunc("/api/stream", d.stream)
	mux.HandleFunc("/api/cost", d.cost)
	mux.HandleFunc("/api/usage", d.usage)
	mux.HandleFunc("/api/run", d.runTimeline)
	mux.HandleFunc("/api/logs", d.logs)
	mux.HandleFunc("/api/repos", d.repos)
	mux.HandleFunc("/api/repos/names", d.repoNames)
	mux.HandleFunc("/api/replay", d.replay)
	mux.HandleFunc("/api/thread", d.thread)
	mux.HandleFunc("/api/ask", d.ask)
	mux.HandleFunc("/api/ask/answer", d.askAnswer)
	mux.HandleFunc("/api/memory", d.tendMemory)
	mux.HandleFunc("/api/files", d.files)
	mux.HandleFunc("/api/source", d.source)
	mux.HandleFunc("/api/save", d.saveFile)
	mux.Handle("/awf/", http.StripPrefix("/awf", proxy))

	go d.watchRuns()

	log.Printf("dots dashboard on http://%s (awf at %s)", *addr, *awf)
	log.Fatal(http.ListenAndServe(*addr, mux))
}

type dash struct {
	state       string
	root        string
	awf         string
	awfgh       string
	transcripts string
	opencode    string
	mu          sync.Mutex
	active      map[string]bool
	tried       map[string]bool
	indexed     map[string]bool

	// The page asks for a price on every refresh; the last answer per run file
	// is reused until the file changes.
	costMu sync.Mutex
	costs  map[string]costMemo
}

// watchRuns records the event stream of every running execution into
// state/logs. awf forgets events once the execution leaves its session
// registry, so a recording has to start while the run is still running.
func (d *dash) watchRuns() {
	lastPrune := time.Time{}
	for {
		d.recordRuns()
		if time.Since(lastPrune) > 6*time.Hour {
			d.pruneLogs()
			lastPrune = time.Now()
		}
		time.Sleep(time.Second)
	}
}

// pruneLogs mirrors the 30 day retention awf keeps for its own history.
func (d *dash) pruneLogs() {
	dir := filepath.Join(d.state, "logs")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-30 * 24 * time.Hour)
	live := map[string]bool{}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			os.Remove(filepath.Join(dir, e.Name()))
			continue
		}
		live[e.Name()] = true
	}
	// The index outlives nothing: drop the lines whose log file is gone.
	var keep strings.Builder
	indexed := map[string]bool{}
	for _, line := range strings.Split(readFile(filepath.Join(dir, "index.jsonl"), 8<<20), "\n") {
		var e logIndex
		if json.Unmarshal([]byte(line), &e) != nil || !live[e.ID+".jsonl"] {
			continue
		}
		keep.WriteString(line + "\n")
		indexed[e.ID] = true
	}
	os.WriteFile(filepath.Join(dir, "index.jsonl"), []byte(keep.String()), 0o644)
	d.mu.Lock()
	d.indexed = indexed
	d.mu.Unlock()
}

// recordRuns records every execution awf knows and has no log for yet. A run
// can be shorter than the poll interval, so a terminal run is fetched once
// too: awf keeps its session for a while after the end.
func (d *dash) recordRuns() {
	var list struct {
		Body struct {
			Executions []struct {
				ID           string `json:"execution_id"`
				Status       string `json:"status"`
				WorkflowName string `json:"workflow_name"`
				StartedAt    string `json:"started_at"`
			} `json:"executions"`
		} `json:"body"`
	}
	resp, err := http.Get(d.awf + "/api/executions")
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if json.NewDecoder(resp.Body).Decode(&list) != nil {
		return
	}
	for _, e := range list.Body.Executions {
		if !logID(e.ID) {
			continue
		}
		if exists(filepath.Join(d.state, "logs", e.ID+".jsonl")) {
			d.noteIndex(e.ID, e.WorkflowName, e.StartedAt)
			continue
		}
		d.mu.Lock()
		if d.active[e.ID] || d.tried[e.ID] {
			d.mu.Unlock()
			continue
		}
		if e.Status == "running" {
			d.active[e.ID] = true
			go d.record(e.ID, e.WorkflowName, e.StartedAt, true)
		} else {
			d.tried[e.ID] = true
			go d.record(e.ID, e.WorkflowName, e.StartedAt, false)
		}
		d.mu.Unlock()
	}
}

func (d *dash) record(id, workflow, started string, retry bool) {
	if retry {
		defer func() {
			d.mu.Lock()
			delete(d.active, id)
			d.mu.Unlock()
		}()
	}
	resp, err := http.Get(d.awf + "/api/executions/" + id + "/events")
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return
	}
	path := filepath.Join(d.state, "logs", id+".jsonl")
	if os.MkdirAll(filepath.Dir(path), 0o755) != nil {
		return
	}
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close()
	io.Copy(f, resp.Body)
	// A session that already served its events returns an empty stream; leave
	// no empty file behind so the page says "no log" instead of a blank box.
	if info, err := f.Stat(); err == nil && info.Size() == 0 {
		os.Remove(path)
		return
	}
	d.noteIndex(id, workflow, started)
}

// stream tails a recorded log to the browser. awf hands the event channel to a
// single reader, this recorder, so a second reader on awf's own stream would
// steal events from it. Tailing the file keeps one consumer and replays what
// already happened, which awf does not.
func (d *dash) stream(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if !logID(id) {
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	// A run can be listed a moment before the recorder opens its file.
	for i := 0; i < 20 && !exists(filepath.Join(d.state, "logs", id+".jsonl")) && d.transcriptPath(id) == ""; i++ {
		time.Sleep(100 * time.Millisecond)
	}
	source, transcript := d.transcriptPath(id), true
	if source == "" {
		source, transcript = filepath.Join(d.state, "logs", id+".jsonl"), false
	}
	f, err := os.Open(source)
	if err != nil {
		writeJSONStatus(w, http.StatusNotFound, map[string]string{"error": "no log recorded"})
		return
	}
	defer f.Close()
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]string{"error": "streaming unsupported"})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	carry := ""
	draining := false
	buf := make([]byte, 32<<10)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			carry += string(buf[:n])
			lines := strings.Split(carry, "\n")
			carry = lines[len(lines)-1]
			for _, line := range lines[:len(lines)-1] {
				frame := line
				if transcript {
					converted, ok := transcriptFrame(line)
					if !ok {
						continue
					}
					frame = converted
				} else if payload, ok := strings.CutPrefix(line, "data: "); ok {
					frame = payload
				} else {
					continue
				}
				if _, err := fmt.Fprintf(w, "data: %s\n\n", frame); err != nil {
					return
				}
				flusher.Flush()
				if !transcript && strings.Contains(frame, `"kind":"workflow.`) {
					return
				}
			}
			continue
		}
		if err != nil && err != io.EOF {
			return
		}
		// The transcript has no terminal record; the recorder's file does.
		if !transcript || d.recordedTerminal(id) {
			if draining {
				return
			}
			draining = true
			time.Sleep(400 * time.Millisecond)
			continue
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// cost reports what one run spent. Since awf v0.12.0 the provider price of an
// agent step travels with the run as `cost_usd`, so the number is read from the
// run's own log: no second database, no time window to guess.
func (d *dash) cost(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if !logID(id) {
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	usd, steps := d.runCost(id)
	writeJSON(w, runCost{Available: true, Cost: usd, Steps: steps})
}

type runCost struct {
	Available bool    `json:"available"`
	Cost      float64 `json:"cost"`
	Steps     int     `json:"steps"`
}

// usageOne is one pull request, or one repository when the run names no pull
// request, with the runs that touched it and what each workflow spent.
type usageOne struct {
	Key       string      `json:"key"`
	Repo      string      `json:"repo"`
	PR        string      `json:"pr,omitempty"`
	Issue     string      `json:"issue,omitempty"`
	Cost      float64     `json:"cost"`
	Runs      int         `json:"runs"`
	Unpriced  int         `json:"unpriced"`
	Workflows []usageFlow `json:"workflows"`
}

type usageFlow struct {
	Workflow string  `json:"workflow"`
	Cost     float64 `json:"cost"`
	Runs     int     `json:"runs"`
}

// usage adds up the price of every recorded run, grouped by pull request or
// issue. The grouping comes from the run log, like replay: the inputs are not
// stored anywhere else. Only runs with a log on disk are counted, so the totals
// cover what the recorder kept.
func (d *dash) usage(w http.ResponseWriter, r *http.Request) {
	type group struct {
		one  usageOne
		flow map[string]*usageFlow
	}
	groups := map[string]*group{}
	var total float64
	var runs, unpriced int

	seen := map[string]bool{}
	for _, line := range strings.Split(readFile(d.indexFile(), 8<<20), "\n") {
		var e logIndex
		if json.Unmarshal([]byte(line), &e) != nil || e.ID == "" || seen[e.ID] {
			continue
		}
		seen[e.ID] = true
		in := replayInputs(d.runText(e.ID))
		repo, pr, issue := in["repo"], in["pr"], in["issue"]
		if pr == "" && issue != "" {
			pr = "issue " + issue
		}
		key := repo
		if repo != "" && pr != "" {
			key = repo + "#" + pr
		}
		if key == "" {
			key = "(no pull request)"
		}
		g, ok := groups[key]
		if !ok {
			g = &group{one: usageOne{Key: key, Repo: repo, PR: pr, Issue: issue}, flow: map[string]*usageFlow{}}
			groups[key] = g
		}
		usd, _ := d.runCost(e.ID)
		g.one.Runs++
		runs++
		g.one.Cost += usd
		total += usd
		if usd == 0 {
			g.one.Unpriced++
			unpriced++
		}
		f, ok := g.flow[e.WorkflowName]
		if !ok {
			f = &usageFlow{Workflow: e.WorkflowName}
			g.flow[e.WorkflowName] = f
		}
		f.Runs++
		f.Cost += usd
	}

	out := []usageOne{}
	for _, g := range groups {
		for _, f := range g.flow {
			g.one.Workflows = append(g.one.Workflows, *f)
		}
		sort.Slice(g.one.Workflows, func(i, j int) bool { return g.one.Workflows[i].Cost > g.one.Workflows[j].Cost })
		out = append(out, g.one)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Cost != out[j].Cost {
			return out[i].Cost > out[j].Cost
		}
		return out[i].Key < out[j].Key
	})
	writeJSON(w, map[string]any{
		"total":    total,
		"runs":     runs,
		"unpriced": unpriced,
		"groups":   out,
	})
}

// runStep is one step of a run with what it cost in wall time. Agent steps
// carry the size of the prompt they were given and of what they answered,
// which is what tells a slow step from a step that reads too much.
type runStep struct {
	Name   string  `json:"name"`
	Kind   string  `json:"kind"`
	Status string  `json:"status"`
	Start  string  `json:"start,omitempty"`
	MS     int64   `json:"ms"`
	Cost   float64 `json:"cost,omitempty"`
	Prompt int     `json:"prompt,omitempty"`
	Answer int     `json:"answer,omitempty"`
	Error  string  `json:"error,omitempty"`
}

// runTimeline reads a run's transcript and reports each step with its
// duration. awf writes the timestamps, so this is measurement, not an opinion:
// a step that spends minutes and a step that reads a huge prompt show up as
// numbers instead of a feeling.
func (d *dash) runTimeline(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if !logID(id) {
		writeJSONStatus(w, 400, map[string]string{"error": "bad id"})
		return
	}
	path := d.transcriptPath(id)
	if path == "" {
		writeJSONStatus(w, 404, map[string]string{"error": "no transcript for this run"})
		return
	}
	steps := []*runStep{}
	index := map[string]*runStep{}
	open := map[string]*runStep{}
	var started time.Time

	for _, line := range strings.Split(readFile(path, 8<<20), "\n") {
		if line == "" {
			continue
		}
		var e transcriptEvent
		if json.Unmarshal([]byte(line), &e) != nil {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, e.Timestamp)
		if err != nil {
			continue
		}
		if started.IsZero() {
			started = at
		}
		switch e.Type {
		case "step.started":
			step := &runStep{Name: e.Payload.Name, Kind: e.Payload.Kind, Start: at.UTC().Format(time.RFC3339), Status: "running"}
			steps = append(steps, step)
			index[e.Payload.Name] = step
			open[e.Payload.Name] = step
		case "step.completed", "step.failed":
			if step, ok := open[e.Payload.Name]; ok {
				step.Status = e.Payload.Status
				step.Cost = e.Payload.CostUSD
				step.Error = e.Payload.Error
				step.MS = at.Sub(mustParse(step.Start)).Milliseconds()
				delete(open, e.Payload.Name)
			}
		case "message.user", "message.assistant":
			// The message belongs to the step that is open when it arrives.
			n := 0
			for _, b := range e.Payload.Blocks {
				n += len(b.Text)
			}
			for _, step := range open {
				if e.Type == "message.user" {
					step.Prompt += n
				} else {
					step.Answer += n
				}
			}
		}
	}
	var total int64
	for _, step := range steps {
		total += step.MS
	}
	writeJSON(w, map[string]any{
		"id":       id,
		"started":  started.UTC().Format(time.RFC3339),
		"total_ms": total,
		"steps":    steps,
	})
}

// mustParse reads back the RFC3339 timestamp a step started at.
func mustParse(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

// costMemo keeps the last answer for one run file, keyed by its size and
// modification time, because the page asks again on every refresh.
type costMemo struct {
	size  int64
	mod   time.Time
	usd   float64
	steps int
}

var costUSDRE = regexp.MustCompile(`"cost_usd":\s*([0-9.eE+-]+)`)

// runCost sums the prices recorded in a run's log. The transcript carries them;
// the recorder file is the fallback, and it carries them too once awf puts the
// field on the stream. A run started by an older awf has no price and reports
// zero, which the page shows as no price.
func (d *dash) runCost(id string) (float64, int) {
	path := d.transcriptPath(id)
	if path == "" {
		path = filepath.Join(d.state, "logs", id+".jsonl")
	}
	st, err := os.Stat(path)
	if err != nil {
		return 0, 0
	}
	d.costMu.Lock()
	memo, ok := d.costs[id]
	d.costMu.Unlock()
	if ok && memo.size == st.Size() && memo.mod.Equal(st.ModTime()) {
		return memo.usd, memo.steps
	}
	var usd float64
	var steps int
	for _, line := range strings.Split(readFile(path, 8<<20), "\n") {
		for _, m := range costUSDRE.FindAllStringSubmatch(line, -1) {
			var v float64
			if _, err := fmt.Sscanf(m[1], "%g", &v); err == nil && v > 0 {
				usd += v
				steps++
			}
		}
	}
	d.costMu.Lock()
	if d.costs == nil {
		d.costs = map[string]costMemo{}
	}
	d.costs[id] = costMemo{size: st.Size(), mod: st.ModTime(), usd: usd, steps: steps}
	d.costMu.Unlock()
	return usd, steps
}

// transcriptEvent is one line of awf's per-run transcript: the same step events
// as the SSE stream, plus the agent messages the stream omits.
type transcriptEvent struct {
	Seq       uint64 `json:"seq"`
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	Payload   struct {
		Name    string  `json:"name"`
		Kind    string  `json:"kind"`
		Status  string  `json:"status"`
		Error   string  `json:"error"`
		CostUSD float64 `json:"cost_usd"`
		Blocks  []struct {
			Text string `json:"text"`
		} `json:"blocks"`
	} `json:"payload"`
}

// transcriptFrame rewrites a transcript line into the frame shape the page
// expects, with the message text the agent produced, cut to keep frames small.
func transcriptFrame(line string) (string, bool) {
	var ev transcriptEvent
	if json.Unmarshal([]byte(line), &ev) != nil || ev.Type == "" {
		return "", false
	}
	status := ev.Payload.Status
	if ev.Type == "step.started" {
		status = "running"
	}
	var text strings.Builder
	for _, b := range ev.Payload.Blocks {
		if t := strings.Join(strings.Fields(b.Text), " "); t != "" {
			text.WriteString(t)
			text.WriteString(" ")
		}
	}
	frame, err := json.Marshal(map[string]any{
		"kind":   ev.Type,
		"seq":    ev.Seq,
		"step":   ev.Payload.Name,
		"status": status,
		"error":  ev.Payload.Error,
		"text":   truncate(strings.TrimSpace(text.String()), 400),
	})
	if err != nil {
		return "", false
	}
	return string(frame), true
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// transcriptPath finds the file awf writes for a run. awf lives either on the
// host (XDG data dir) or in a container that shares state/, hence the list.
func (d *dash) transcriptPath(id string) string {
	for _, dir := range d.transcriptDirs() {
		if p := filepath.Join(dir, id+".jsonl"); exists(p) {
			return p
		}
	}
	return ""
}

func (d *dash) transcriptDirs() []string {
	if d.transcripts != "" {
		return []string{d.transcripts}
	}
	dirs := []string{filepath.Join(d.state, "awf", "transcripts")}
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		dirs = append(dirs, filepath.Join(xdg, "awf", "transcripts"))
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		dirs = append(dirs, filepath.Join(home, ".local", "share", "awf", "transcripts"))
	}
	return dirs
}

// recordedTerminal reports whether the recorder already saw the end of the run.
func (d *dash) recordedTerminal(id string) bool {
	b, err := os.ReadFile(filepath.Join(d.state, "logs", id+".jsonl"))
	if err != nil {
		return false
	}
	if len(b) > 4096 {
		b = b[len(b)-4096:]
	}
	return strings.Contains(string(b), `"kind":"workflow.`)
}

// logID keeps the id from reaching the filesystem as a path.
func logID(id string) bool {
	if len(id) < 8 || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}

// replayRun answers /api/replay: what a finished run was asked to do, so the
// page can offer the same run again. awf keeps no copy of the inputs, so the
// values come either from the run log (see replayInputs) or from the page's own
// record written when it started the run (state/runs/<id>.json).
type replayRun struct {
	Workflow string            `json:"workflow"`
	Scope    string            `json:"scope"`
	Inputs   map[string]string `json:"inputs"`
	Source   string            `json:"source"`
}

// replayInputs reads the inputs back from a run log. Every workflow spells the
// target in its prompts ("owner/repo#123", "issue #7") and review runs also
// write the head commit as "pr123-<sha>", so the log holds what the run was
// given. It is a guess: the page shows it as one and the operator can correct it.
func replayInputs(text string) map[string]string {
	// Transcript lines are JSON: the prompt keeps its escapes, and a bare `\n`
	// would glue an `n` onto the repository name.
	text = strings.NewReplacer(`\n`, " ", `\t`, " ", `\"`, `"`).Replace(text)
	out := map[string]string{}
	if m := repoIssueRE.FindStringSubmatch(text); m != nil {
		out["repo"] = m[1] + "/" + m[2]
		out["pr"] = m[3]
	}
	if m := prShaRE.FindStringSubmatch(text); m != nil {
		out["sha"] = m[2]
		if out["pr"] == "" {
			out["pr"] = m[1]
		}
	}
	if idx := issueRE.FindStringSubmatchIndex(text); idx != nil {
		out["issue"] = text[idx[2]:idx[3]]
		// fix-issue names the repository before the issue ("in owner/repo,
		// issue #7") instead of as owner/repo#7.
		if out["repo"] == "" {
			from := idx[0] - 200
			if from < 0 {
				from = 0
			}
			if r := namedRepoRE.FindStringSubmatch(text[from:idx[0]]); r != nil {
				out["repo"] = r[1]
			}
		}
	}
	return out
}

var (
	workflowNameRE = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	repoIssueRE    = regexp.MustCompile(`([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)#([0-9]+)`)
	prShaRE        = regexp.MustCompile(`\bpr([0-9]+)-([0-9a-f]{7,40})\b`)
	namedRepoRE    = regexp.MustCompile(`\b([A-Za-z0-9][A-Za-z0-9_.-]*/[A-Za-z0-9][A-Za-z0-9_.-]*)`)
	issueRE        = regexp.MustCompile(`\bissue #([0-9]+)`)
)

// runText returns the log of a run: the transcript first, since only it holds
// the agent prompts, then the recorder file.
func (d *dash) runText(id string) string {
	if p := d.transcriptPath(id); p != "" {
		return readFile(p, 4<<20)
	}
	return readFile(filepath.Join(d.state, "logs", id+".jsonl"), 1<<20)
}

func (d *dash) indexEntry(id string) (logIndex, bool) {
	for _, line := range strings.Split(readFile(d.indexFile(), 8<<20), "\n") {
		var e logIndex
		if json.Unmarshal([]byte(line), &e) == nil && e.ID == id {
			return e, true
		}
	}
	return logIndex{}, false
}

// scopeOf asks awf where a workflow lives, falling back to global: every local
// workflow of this repository is registered there.
func (d *dash) scopeOf(name string) string {
	client := &http.Client{Timeout: 900 * time.Millisecond}
	resp, err := client.Get(d.awf + "/api/workflows")
	if err != nil {
		return "global"
	}
	defer resp.Body.Close()
	var out struct {
		Workflows []struct {
			Name  string `json:"name"`
			Scope string `json:"scope"`
		} `json:"workflows"`
	}
	if json.NewDecoder(resp.Body).Decode(&out) != nil {
		return "global"
	}
	for _, w := range out.Workflows {
		if w.Name == name && w.Scope != "" {
			return w.Scope
		}
	}
	return "global"
}

func (d *dash) replay(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id != "" && !logID(id) {
		writeJSONStatus(w, 400, map[string]string{"error": "bad run id"})
		return
	}
	out := replayRun{Inputs: map[string]string{}, Source: "none"}
	if entry, ok := d.indexEntry(id); ok {
		out.Workflow = entry.WorkflowName
	}
	if out.Workflow == "" {
		if name := r.URL.Query().Get("workflow"); workflowNameRE.MatchString(name) {
			out.Workflow = name
		}
	}
	if out.Workflow == "" {
		writeJSONStatus(w, 404, map[string]string{"error": "no workflow recorded for this run"})
		return
	}
	out.Scope = d.scopeOf(out.Workflow)
	if id != "" {
		if guess := replayInputs(d.runText(id)); len(guess) > 0 {
			out.Inputs = guess
			out.Source = "log"
		}
	}
	writeJSON(w, out)
}

// The Ask chat: one conversation per pull request, stored beside the run
// artifacts. A message becomes a run of the ask-pr workflow, whose answer.md
// comes back here as the next assistant message.
type threadMessage struct {
	Role    string       `json:"role"`
	Text    string       `json:"text"`
	At      string       `json:"at"`
	RunID   string       `json:"run_id,omitempty"`
	Cost    float64      `json:"cost,omitempty"`
	Suggest []suggestion `json:"suggest,omitempty"`
}

// suggestion is a workflow the agent named as the next step. The page turns
// each one into a button; nothing runs until the operator clicks it.
type suggestion struct {
	Workflow string            `json:"workflow"`
	Inputs   map[string]string `json:"inputs,omitempty"`
}

var (
	suggestRE  = regexp.MustCompile(`^SUGGEST:\s*(\S*)\s*(.*)$`)
	suggestKV  = regexp.MustCompile(`([A-Za-z0-9_]+)=(?:"([^"]*)"|(\S+))`)
	suggestMax = 3
)

// parseSuggest splits the answer into the text a reader sees and the workflows
// it proposes. The agent writes one line per suggestion, at the end:
//
//	SUGGEST: reply-pr repo=owner/repo pr=42
func parseSuggest(text string) (string, []suggestion) {
	kept := []string{}
	var out []suggestion
	for _, line := range strings.Split(text, "\n") {
		m := suggestRE.FindStringSubmatch(line)
		if m == nil {
			kept = append(kept, line)
			continue
		}
		// The line never reaches the reader, even when its workflow name is
		// refused: a suggestion the page cannot render is noise in the answer.
		if len(out) >= suggestMax || !workflowNameRE.MatchString(m[1]) {
			continue
		}
		inputs := map[string]string{}
		for _, kv := range suggestKV.FindAllStringSubmatch(m[2], -1) {
			v := kv[2]
			if v == "" {
				v = kv[3]
			}
			inputs[kv[1]] = v
		}
		if len(inputs) == 0 {
			inputs = nil
		}
		out = append(out, suggestion{Workflow: m[1], Inputs: inputs})
	}
	return strings.TrimSpace(strings.Join(kept, "\n")), out
}

type thread struct {
	Repo     string          `json:"repo"`
	PR       string          `json:"pr"`
	Messages []threadMessage `json:"messages"`
}

var (
	asksMu        sync.Mutex
	prNumberRE    = regexp.MustCompile(`^[0-9]{1,7}$`)
	askMaxSize    = 16 << 10
	askQuestionTo = 8000
)

// prNumber reads the pull request number out of an artifact directory name
// such as "pr447" or "pr447-<sha>".
func prNumber(dir string) string {
	base := strings.TrimPrefix(dir, "pr")
	if i := strings.IndexByte(base, '-'); i >= 0 {
		base = base[:i]
	}
	if prNumberRE.MatchString(base) {
		return base
	}
	return ""
}

// repoFromURL keeps "owner/repo" out of a pull request URL.
func repoFromURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 || parts[2-1] == "" {
		return ""
	}
	repo := parts[0] + "/" + parts[1]
	if !repoNameRE.MatchString(repo) {
		return ""
	}
	return repo
}

func (d *dash) askDir(repo, pr string) string {
	return filepath.Join(d.state, "asks", filepath.FromSlash(repo), "pr"+pr)
}

func (d *dash) loadThread(repo, pr string) thread {
	out := thread{Repo: repo, PR: pr, Messages: []threadMessage{}}
	loadJSON(filepath.Join(d.askDir(repo, pr), "thread.json"), &out)
	if out.Messages == nil {
		out.Messages = []threadMessage{}
	}
	out.Repo, out.PR = repo, pr
	return out
}

// writeThread stores the conversation twice: thread.json for the page and
// thread.md for the agent, which reads markdown with its read tool.
func (d *dash) writeThread(t thread) error {
	dir := d.askDir(t.Repo, t.PR)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(thread{Repo: t.Repo, PR: t.PR, Messages: t.Messages}, "", "  ")
	if err != nil {
		return err
	}
	if err := atomicWrite(filepath.Join(dir, "thread.json"), append(b, '\n')); err != nil {
		return err
	}
	var md strings.Builder
	md.WriteString("# Conversation about " + t.Repo + "#" + t.PR + "\n")
	for _, m := range t.Messages {
		md.WriteString("\n## " + m.Role + " · " + m.At + "\n\n" + m.Text + "\n")
	}
	return atomicWrite(filepath.Join(dir, "thread.md"), []byte(md.String()))
}

func (d *dash) thread(w http.ResponseWriter, r *http.Request) {
	repo, pr := r.URL.Query().Get("repo"), r.URL.Query().Get("pr")
	if !repoNameRE.MatchString(repo) || !prNumberRE.MatchString(pr) {
		writeJSONStatus(w, 400, map[string]string{"error": "repo and pr are required"})
		return
	}
	writeJSON(w, d.loadThread(repo, pr))
}

// startRun posts a run request to awf and returns the execution id.
func (d *dash) startRun(workflow string, inputs map[string]string) (string, error) {
	body, err := json.Marshal(map[string]any{"inputs": inputs})
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 30 * time.Second}
	u := d.awf + "/api/workflows/" + url.PathEscape(d.scopeOf(workflow)) + "/" + url.PathEscape(workflow) + "/run"
	resp, err := client.Post(u, "application/json", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusAccepted {
		return "", fmt.Errorf("awf refused the run: %s", strings.TrimSpace(string(raw)))
	}
	var out struct {
		Body struct {
			ExecutionID string `json:"execution_id"`
		} `json:"body"`
	}
	if json.Unmarshal(raw, &out) != nil || out.Body.ExecutionID == "" {
		return "", fmt.Errorf("awf accepted the run but named no execution")
	}
	return out.Body.ExecutionID, nil
}

func (d *dash) ask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONStatus(w, 405, map[string]string{"error": "POST only"})
		return
	}
	if !loopbackRequest(r) {
		writeJSONStatus(w, 403, map[string]string{"error": "loopback only"})
		return
	}
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		writeJSONStatus(w, 415, map[string]string{"error": "JSON only"})
		return
	}
	var in struct {
		Repo     string `json:"repo"`
		PR       string `json:"pr"`
		Question string `json:"question"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, int64(askMaxSize))).Decode(&in) != nil {
		writeJSONStatus(w, 400, map[string]string{"error": "unreadable body"})
		return
	}
	in.Question = strings.TrimSpace(in.Question)
	if !repoNameRE.MatchString(in.Repo) || !prNumberRE.MatchString(in.PR) || in.Question == "" {
		writeJSONStatus(w, 422, map[string]string{"error": "repo, pr and question are required"})
		return
	}
	if len(in.Question) > askQuestionTo {
		writeJSONStatus(w, 413, map[string]string{"error": fmt.Sprintf("question is longer than %d characters", askQuestionTo)})
		return
	}

	asksMu.Lock()
	defer asksMu.Unlock()
	t := d.loadThread(in.Repo, in.PR)
	msg := threadMessage{Role: "user", Text: in.Question, At: time.Now().UTC().Format(time.RFC3339)}
	t.Messages = append(t.Messages, msg)
	if err := d.writeThread(t); err != nil {
		writeJSONStatus(w, 500, map[string]string{"error": err.Error()})
		return
	}
	id, err := d.startRun("ask-pr", map[string]string{"repo": in.Repo, "pr": in.PR, "question": in.Question})
	if err != nil {
		// The question never reached awf: keep the thread as it was.
		t.Messages = t.Messages[:len(t.Messages)-1]
		d.writeThread(t)
		writeJSONStatus(w, 502, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"execution_id": id, "message": msg})
}

// askAnswer moves the answer.md a run wrote into the thread. It is idempotent:
// a run id lands in the thread once, so a retried request adds nothing.
// runCostSince prices one answer: awf records what each agent step cost, and
// the transcript of this run carries it. Zero means no price is known (a run
// from an awf without the field, or no agent step), never free.
func (d *dash) runCostSince(t thread, run string) float64 {
	if run == "" {
		return 0
	}
	usd, _ := d.runCost(run)
	return usd
}

func (d *dash) askAnswer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONStatus(w, 405, map[string]string{"error": "POST only"})
		return
	}
	if !loopbackRequest(r) {
		writeJSONStatus(w, 403, map[string]string{"error": "loopback only"})
		return
	}
	var in struct {
		Repo string `json:"repo"`
		PR   string `json:"pr"`
		Run  string `json:"run"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil {
		writeJSONStatus(w, 400, map[string]string{"error": "unreadable body"})
		return
	}
	if !repoNameRE.MatchString(in.Repo) || !prNumberRE.MatchString(in.PR) || !logID(in.Run) {
		writeJSONStatus(w, 422, map[string]string{"error": "repo, pr and run are required"})
		return
	}

	asksMu.Lock()
	defer asksMu.Unlock()
	dir := d.askDir(in.Repo, in.PR)
	t := d.loadThread(in.Repo, in.PR)
	for _, m := range t.Messages {
		if m.RunID == in.Run && m.Role == "assistant" {
			writeJSON(w, m)
			return
		}
	}
	text := strings.TrimSpace(readFile(filepath.Join(dir, "answer.md"), 1<<20))
	if text == "" {
		writeJSONStatus(w, 404, map[string]string{"error": "this run wrote no answer.md"})
		return
	}
	shown, suggest := parseSuggest(text)
	msg := threadMessage{Role: "assistant", Text: shown, At: time.Now().UTC().Format(time.RFC3339), RunID: in.Run, Cost: d.runCostSince(t, in.Run), Suggest: suggest}
	t.Messages = append(t.Messages, msg)
	if err := d.writeThread(t); err != nil {
		writeJSONStatus(w, 500, map[string]string{"error": err.Error()})
		return
	}
	// The next turn starts from a clean file, so a stale answer is never read
	// twice.
	os.Remove(filepath.Join(dir, "answer.md"))
	writeJSON(w, msg)
}

func (d *dash) repos(w http.ResponseWriter, r *http.Request) {
	var cfg struct {
		Repos []ghEntry `yaml:"repos"`
	}
	out := []watchedRepo{}
	if yaml.Unmarshal([]byte(readFile(d.awfgh, 1<<20)), &cfg) != nil {
		writeJSON(w, out)
		return
	}
	idx := map[string]int{}
	tower, paths := towerEnabled(d.opencode), repoPaths(filepath.Join(d.root, "repos.map"))
	for _, e := range cfg.Repos {
		i, ok := idx[e.Repo]
		if !ok {
			i = len(out)
			idx[e.Repo] = i
			out = append(out, watchedRepo{Repo: e.Repo, Entries: []ghEntry{}, Seen: []string{}})
		}
		out[i].Entries = append(out[i].Entries, e)
		// Only a repository with a local checkout can be checked: the runner
		// clones the others elsewhere.
		if tower && paths[e.Repo] != "" {
			out[i].TowerIgnore = towerIgnoreState(paths[e.Repo])
		}
	}
	lines := strings.Split(readFile(filepath.Join(d.state, "awf-gh", "seen.jsonl"), 1<<20), "\n")
	for _, s := range dedupeSeen(lines) {
		if i, ok := idx[s.Repo]; ok {
			out[i].Seen = append(out[i].Seen, s.PR)
		}
	}
	writeJSON(w, out)
}

// towerEnabled reports whether the opencode config declares a tower MCP server
// that is not disabled. The file is JSONC, so the entry is scanned rather than
// parsed.
//
// shortcut: a comment or a nested object inside that entry would throw the scan
// off; switch to a JSONC parser if the config grows one.
var (
	towerEntryRE = regexp.MustCompile(`(?s)"tower"\s*:\s*\{(.*?)\}`)
	towerOffRE   = regexp.MustCompile(`"disabled"\s*:\s*true`)
)

func towerEnabled(path string) bool {
	m := towerEntryRE.FindStringSubmatch(readFile(path, 1<<20))
	return m != nil && !towerOffRE.MatchString(m[1])
}

// towerIgnoreState says what `.towerignore` in dir still needs: "missing" when
// the file is absent, "empty" when it holds no pattern, "" when it is fine.
func towerIgnoreState(dir string) string {
	body, err := os.ReadFile(filepath.Join(dir, ".towerignore"))
	if err != nil {
		return "missing"
	}
	for _, line := range strings.Split(string(body), "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
			return ""
		}
	}
	return "empty"
}

// repoPaths reads repos.map: one `owner/repo=/absolute/checkout` per line.
func repoPaths(path string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(readFile(path, 1<<20), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if i := strings.IndexByte(line, '='); i > 0 {
			out[strings.TrimSpace(line[:i])] = strings.TrimSpace(line[i+1:])
		}
	}
	return out
}

// defaultOpencodeConfig finds the config that declares the MCP servers.
func defaultOpencodeConfig() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "opencode", "opencode.jsonc")
}

var repoNameRE = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// repoNames suggests the repositories this machine already knows: the watcher
// config, the daily list, the path map and the directories a pass wrote under
// state/tend. The run dialog offers them, and still accepts any other name.
func (d *dash) repoNames(w http.ResponseWriter, r *http.Request) {
	seen := map[string]bool{}
	add := func(s string) {
		s = strings.TrimSpace(s)
		if repoNameRE.MatchString(s) {
			seen[s] = true
		}
	}
	var cfg struct {
		Repos []ghEntry `yaml:"repos"`
	}
	if yaml.Unmarshal([]byte(readFile(d.awfgh, 1<<20)), &cfg) == nil {
		for _, e := range cfg.Repos {
			add(e.Repo)
		}
	}
	for _, line := range strings.Split(readFile(filepath.Join(d.root, "tend.list"), 1<<20), "\n") {
		if fields := strings.Fields(line); len(fields) > 0 {
			add(fields[0])
		}
	}
	for _, line := range strings.Split(readFile(filepath.Join(d.root, "repos.map"), 1<<20), "\n") {
		if i := strings.IndexByte(line, '='); i > 0 {
			add(line[:i])
		}
	}
	owners, _ := os.ReadDir(filepath.Join(d.state, "tend"))
	for _, owner := range owners {
		if !owner.IsDir() {
			continue
		}
		repos, _ := os.ReadDir(filepath.Join(d.state, "tend", owner.Name()))
		for _, repo := range repos {
			if repo.IsDir() {
				add(owner.Name() + "/" + repo.Name())
			}
		}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	writeJSON(w, out)
}

func (d *dash) log(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if !logID(id) {
		http.NotFound(w, r)
		return
	}
	serveFile(w, r, filepath.Join(d.state, "logs", id+".jsonl"), "text/plain; charset=utf-8")
}

// logIndex maps a recorded execution to the workflow and start time awf
// reports in its history. History ids differ from execution ids, so the page
// joins on workflow plus start second.
type logIndex struct {
	ID           string `json:"execution_id"`
	WorkflowName string `json:"workflow_name"`
	StartedAt    string `json:"started_at"`
}

func (d *dash) indexFile() string {
	return filepath.Join(d.state, "logs", "index.jsonl")
}

func (d *dash) loadIndexed() map[string]bool {
	out := map[string]bool{}
	for _, line := range strings.Split(readFile(d.indexFile(), 8<<20), "\n") {
		var e logIndex
		if json.Unmarshal([]byte(line), &e) == nil && e.ID != "" {
			out[e.ID] = true
		}
	}
	return out
}

func (d *dash) noteIndex(id, workflow, started string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.indexed[id] {
		return
	}
	d.indexed[id] = true
	line, _ := json.Marshal(logIndex{ID: id, WorkflowName: workflow, StartedAt: started})
	f, err := os.OpenFile(d.indexFile(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	f.Write(append(line, '\n'))
}

func (d *dash) logs(w http.ResponseWriter, r *http.Request) {
	// Each row carries its price so the history table needs no second request.
	type row struct {
		logIndex
		Cost float64 `json:"cost,omitempty"`
	}
	out := []row{}
	for _, line := range strings.Split(readFile(d.indexFile(), 8<<20), "\n") {
		var e logIndex
		if json.Unmarshal([]byte(line), &e) == nil && e.ID != "" {
			usd, _ := d.runCost(e.ID)
			out = append(out, row{logIndex: e, Cost: usd})
		}
	}
	writeJSON(w, out)
}

type memoryEntry struct {
	Repo       string `json:"repo"`
	Since      string `json:"since"`
	Lessons    string `json:"lessons"`
	Memory     string `json:"memory"`
	WouldIssue string `json:"wouldIssue"`
	WouldPR    string `json:"wouldPr"`
}

// tendMemory shows what the daily pass wrote per repository: the rules in
// lessons.md and the facts in memory.md.
func (d *dash) tendMemory(w http.ResponseWriter, r *http.Request) {
	root := filepath.Join(d.state, "tend")
	out := []memoryEntry{}
	for _, owner := range listDirs(root) {
		for _, name := range listDirs(filepath.Join(root, owner)) {
			dir := filepath.Join(root, owner, name)
			e := memoryEntry{
				Repo:    owner + "/" + name,
				Since:   strings.TrimSpace(readFile(filepath.Join(dir, "since"), 64)),
				Lessons: readFile(filepath.Join(dir, "lessons.md"), 1<<17),
				Memory:  readFile(filepath.Join(dir, "memory.md"), 1<<17),
				// What the last dry run described but did not open.
				WouldIssue: readFile(filepath.Join(dir, "would-issue.md"), 1<<16),
				WouldPR:    readFile(filepath.Join(dir, "would-pr.md"), 1<<16),
			}
			if e.Lessons == "" && e.Memory == "" && e.WouldIssue == "" && e.WouldPR == "" {
				continue
			}
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Repo < out[j].Repo })
	writeJSON(w, out)
}

func (d *dash) artifacts(w http.ResponseWriter, r *http.Request) {
	out := artifacts{Reviews: []review{}, Replies: []reply{}, Seen: []seen{}}
	for _, name := range listDirs(filepath.Join(d.state, "reviews")) {
		dir := filepath.Join(d.state, "reviews", name)
		var meta struct {
			Title  string `json:"title"`
			Author struct {
				Login string `json:"login"`
			} `json:"author"`
			Base  string `json:"baseRefName"`
			State string `json:"state"`
			URL   string `json:"url"`
		}
		loadJSON(filepath.Join(dir, "meta.json"), &meta)
		diff := readFile(filepath.Join(dir, "diff.patch"), 1<<20)
		repo := strings.TrimSpace(readFile(filepath.Join(dir, "repo"), 200))
		pr := prNumber(name)
		out.Reviews = append(out.Reviews, review{
			ID:        name,
			Repo:      repo,
			PR:        pr,
			Title:     meta.Title,
			Author:    meta.Author.Login,
			Base:      meta.Base,
			HasReview: exists(filepath.Join(dir, "review.md")),
			DiffLines: strings.Count(diff, "\n"),
			State:     meta.State,
			URL:       reviewURL(meta.URL, repo, pr),
		})
	}
	for _, name := range listDirs(filepath.Join(d.state, "replies")) {
		dir := filepath.Join(d.state, "replies", name)
		var pr struct {
			Title  string `json:"title"`
			URL    string `json:"url"`
			Author struct {
				Login string `json:"login"`
			} `json:"author"`
			Branch   string            `json:"headRefName"`
			State    string            `json:"state"`
			Comments []json.RawMessage `json:"comments"`
		}
		loadJSON(filepath.Join(dir, "pr.json"), &pr)
		out.Replies = append(out.Replies, reply{
			ID:       name,
			Repo:     repoFromURL(pr.URL),
			PR:       prNumber(name),
			Title:    pr.Title,
			URL:      pr.URL,
			Author:   pr.Author.Login,
			Branch:   pr.Branch,
			Comments: len(pr.Comments),
			HasReply: exists(filepath.Join(dir, "reply.md")),
			Pushed:   strings.TrimSpace(readFile(filepath.Join(dir, "pushed.sha"), 100)),
			State:    pr.State,
		})
	}
	lines := strings.Split(readFile(filepath.Join(d.state, "awf-gh", "seen.jsonl"), 1<<20), "\n")
	out.Seen = dedupeSeen(lines)
	writeJSON(w, out)
}

// reviewURL prefers what gh reported and rebuilds the link for artifacts
// written before meta.json carried url.
func reviewURL(url, repo, pr string) string {
	if url != "" {
		return url
	}
	if repo == "" || pr == "" {
		return ""
	}
	return "https://github.com/" + repo + "/pull/" + pr
}

// The watcher appends a line per poll, so keep one entry per PR.
func dedupeSeen(lines []string) []seen {
	out := []seen{}
	at := map[string]int{}
	for _, line := range lines {
		parts := strings.Split(strings.TrimSpace(line), "#")
		if len(parts) < 2 || parts[0] == "" {
			continue
		}
		entry := seen{Repo: parts[0], PR: parts[1]}
		if i, ok := at[entry.Repo+"#"+entry.PR]; ok {
			out[i] = entry
		} else {
			at[entry.Repo+"#"+entry.PR] = len(out)
			out = append(out, entry)
		}
	}
	return out
}

func (d *dash) file(w http.ResponseWriter, r *http.Request) {
	rel := filepath.Clean("/" + r.URL.Query().Get("path"))
	parts := strings.Split(strings.TrimPrefix(rel, "/"), "/")
	if len(parts) < 2 || !artifactDirs[parts[0]] {
		http.Error(w, "refused", http.StatusNotFound)
		return
	}
	ctype, ok := artifactExt[filepath.Ext(rel)]
	if !ok {
		http.Error(w, "refused", http.StatusNotFound)
		return
	}
	path := filepath.Join(d.state, rel)
	if !strings.HasPrefix(path, filepath.Join(d.state)+string(os.PathSeparator)) {
		http.Error(w, "refused", http.StatusNotFound)
		return
	}
	serveFile(w, r, path, ctype)
}

// --- Editable files -------------------------------------------------------
//
// The dashboard may read and replace the workflow definitions and the local
// watcher configs. It writes files, never git: the repository is the history.
// Every path goes through resolveEditable, and every write proves it came from
// this page on this host.

const maxEditSize = 1 << 20

var workflowFileRE = regexp.MustCompile(`^[A-Za-z0-9_-]+\.yaml$`)

// localFiles are the configs that live in the repository root.
var localFiles = []struct{ Path, Label, Kind string }{
	{"awf-gh.yaml", "awf-gh.yaml", "watcher"},
	{"tend.list", "tend.list", "list"},
	{"repos.map", "repos.map", "map"},
}

type editableFile struct {
	Path    string `json:"path"`
	Label   string `json:"label"`
	Kind    string `json:"kind"`
	Exists  bool   `json:"exists"`
	Size    int64  `json:"size"`
	ModTime string `json:"mod_time"`
}

// resolveEditable maps a page-supplied path to an absolute path inside the
// repository. It returns false for anything outside workflows/ and the three
// known local files; no traversal, no symlinked extension, no hidden files.
func (d *dash) resolveEditable(rel string) (string, bool) {
	// Reject traversal before cleaning: filepath.Clean would fold
	// "../awf-gh.yaml" back into a valid name.
	if rel == "" || filepath.IsAbs(rel) || strings.Contains(rel, `\`) {
		return "", false
	}
	for _, seg := range strings.Split(rel, "/") {
		if seg == ".." {
			return "", false
		}
	}
	rel = strings.TrimPrefix(filepath.Clean(rel), "./")
	for _, f := range localFiles {
		if rel == f.Path {
			return filepath.Join(d.root, rel), true
		}
	}
	if name, ok := strings.CutPrefix(rel, "workflows/"); ok && workflowFileRE.MatchString(name) {
		return filepath.Join(d.root, "workflows", name), true
	}
	return "", false
}

func (d *dash) files(w http.ResponseWriter, r *http.Request) {
	out := []editableFile{}
	for _, name := range listFiles(filepath.Join(d.root, "workflows"), ".yaml") {
		rel := "workflows/" + name
		out = append(out, editableFile{Path: rel, Label: strings.TrimSuffix(name, ".yaml"), Kind: "workflow"})
	}
	for _, f := range localFiles {
		out = append(out, editableFile{Path: f.Path, Label: f.Label, Kind: f.Kind})
	}
	for i := range out {
		abs, _ := d.resolveEditable(out[i].Path)
		if info, err := os.Stat(abs); err == nil && !info.IsDir() {
			out[i].Exists = true
			out[i].Size = info.Size()
			out[i].ModTime = info.ModTime().UTC().Format(time.RFC3339)
		}
	}
	writeJSON(w, out)
}

func (d *dash) source(w http.ResponseWriter, r *http.Request) {
	abs, ok := d.resolveEditable(r.URL.Query().Get("path"))
	if !ok {
		http.Error(w, "refused", http.StatusForbidden)
		return
	}
	serveFile(w, r, abs, "text/plain; charset=utf-8")
}

func (d *dash) saveFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// A cross-site form can POST text/plain, so require JSON and a loopback
	// origin: the first needs a preflight no foreign page can pass, the second
	// blocks a rebinding host that resolves to 127.0.0.1.
	if !loopbackRequest(r) {
		http.Error(w, "refused origin", http.StatusForbidden)
		return
	}
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		http.Error(w, "expected application/json", http.StatusUnsupportedMediaType)
		return
	}
	var req struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxEditSize+4096)).Decode(&req); err != nil {
		http.Error(w, "bad body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if len(req.Content) > maxEditSize {
		http.Error(w, "file too large", http.StatusRequestEntityTooLarge)
		return
	}
	abs, ok := d.resolveEditable(req.Path)
	if !ok {
		http.Error(w, "refused path", http.StatusForbidden)
		return
	}
	// Refuse YAML that does not parse: a broken watcher config or workflow
	// would stop the next polling pass, and the page cannot tell the user when
	// awf-gh runs. The edit stays in the textarea; nothing hits the disk.
	if strings.HasSuffix(abs, ".yaml") {
		var probe any
		if err := yaml.Unmarshal([]byte(req.Content), &probe); err != nil {
			writeJSONStatus(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
			return
		}
	}
	backup, err := backupFile(abs)
	if err != nil {
		http.Error(w, "backup failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := atomicWrite(abs, []byte(req.Content)); err != nil {
		http.Error(w, "write failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"path": req.Path, "bytes": len(req.Content), "backup": backup})
}

// loopbackRequest accepts a request whose Host and Origin are this machine.
// An absent Origin is fine: curl and Fetch from the page send one, but a
// same-origin navigation may not.
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
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// backupFile copies the previous content beside the original, stamped, and
// keeps the last 20 per file. A first write has nothing to copy.
func backupFile(abs string) (string, error) {
	info, err := os.Stat(abs)
	if err != nil || info.IsDir() {
		return "", nil
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	name := filepath.Base(abs) + "." + time.Now().Format("20060102-150405") + ".bak"
	path := filepath.Join(filepath.Dir(abs), name)
	if err := os.WriteFile(path, data, info.Mode().Perm()); err != nil {
		return "", err
	}
	pruneBackups(abs, 20)
	return name, nil
}

func pruneBackups(abs string, keep int) {
	matches, _ := filepath.Glob(abs + ".*.bak")
	sort.Strings(matches)
	for len(matches) > keep {
		os.Remove(matches[0])
		matches = matches[1:]
	}
}

func atomicWrite(abs string, data []byte) error {
	perm := os.FileMode(0o644)
	if info, err := os.Stat(abs); err == nil {
		perm = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(abs), ".save-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, perm); err != nil {
		return err
	}
	return os.Rename(name, abs)
}

// safeAsset serves only the flat files the page ships, plus ui/views/*.js.
// No traversal, no deeper nesting.
func safeAsset(name string) bool {
	if rest, ok := strings.CutPrefix(name, "views/"); ok {
		name = rest
	} else if strings.ContainsAny(name, `/\`) {
		return false
	}
	if name == "" || strings.ContainsAny(name, `/\`) || strings.HasPrefix(name, ".") {
		return false
	}
	switch filepath.Ext(name) {
	case ".js", ".css", ".svg", ".png", ".ico", ".webmanifest":
		return true
	}
	return false
}

func assetType(name string) string {
	switch filepath.Ext(name) {
	case ".js":
		return "text/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".png":
		return "image/png"
	case ".ico":
		return "image/x-icon"
	case ".webmanifest":
		return "application/manifest+json"
	}
	return "application/octet-stream"
}

func listFiles(root, ext string) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ext) && !strings.HasSuffix(e.Name(), ".bak") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

func writeJSONStatus(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func serveFile(w http.ResponseWriter, r *http.Request, path, ctype string) {
	f, err := os.Open(path)
	if err != nil {
		http.Error(w, "missing", http.StatusNotFound)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		http.Error(w, "missing", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, info.Name(), info.ModTime(), f)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("encode: %v", err)
	}
}

func listDirs(root string) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

func loadJSON(path string, v any) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	json.Unmarshal(data, v)
}

func readFile(path string, limit int64) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	buf := make([]byte, limit)
	n, _ := f.Read(buf)
	return string(buf[:n])
}

func exists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// projectRoot finds the repo root: bin/dashboard sits one level below it.
func projectRoot() string {
	exe, err := os.Executable()
	if err == nil {
		dir := filepath.Dir(exe)
		if filepath.Base(dir) == "bin" {
			return filepath.Dir(dir)
		}
	}
	cwd, _ := os.Getwd()
	return cwd
}
