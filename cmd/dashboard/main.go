// Command dashboard serves a loopback page for what opencode-dots does: awf
// runs, PR review and reply artifacts, and the watcher's seen list.
//
// It also proxies /awf/* to the awf API so the page stays same-origin and needs
// no CORS. It binds 127.0.0.1 only. Writes are limited to state/logs/ and the
// four local config files (see /api/save and ADR 0004).
package main

import (
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
	Title     string `json:"title"`
	Author    string `json:"author"`
	Base      string `json:"base"`
	HasReview bool   `json:"has_review"`
	DiffLines int    `json:"diff_lines"`
}

type reply struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	URL      string `json:"url"`
	Author   string `json:"author"`
	Branch   string `json:"branch"`
	Comments int    `json:"comments"`
	HasReply bool   `json:"has_reply"`
	Pushed   string `json:"pushed"`
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

	d := &dash{state: *state, root: *repoRoot, awf: *awf, awfgh: *awfgh, active: map[string]bool{}, tried: map[string]bool{}}
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
	mux.HandleFunc("/api/logs", d.logs)
	mux.HandleFunc("/api/repos", d.repos)
	mux.HandleFunc("/api/repos/names", d.repoNames)
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
	state   string
	root    string
	awf     string
	awfgh   string
	mu      sync.Mutex
	active  map[string]bool
	tried   map[string]bool
	indexed map[string]bool
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
	path := filepath.Join(d.state, "logs", id+".jsonl")
	// A run can be listed a moment before the recorder opens its file.
	for i := 0; i < 20 && !exists(path); i++ {
		time.Sleep(100 * time.Millisecond)
	}
	f, err := os.Open(path)
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
	buf := make([]byte, 32<<10)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			carry += string(buf[:n])
			lines := strings.Split(carry, "\n")
			carry = lines[len(lines)-1]
			for _, line := range lines[:len(lines)-1] {
				payload, ok := strings.CutPrefix(line, "data: ")
				if !ok {
					continue
				}
				if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
					return
				}
				flusher.Flush()
				if strings.Contains(payload, `"kind":"workflow.`) {
					return
				}
			}
			continue
		}
		if err != nil && err != io.EOF {
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(250 * time.Millisecond):
		}
	}
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
	for _, e := range cfg.Repos {
		i, ok := idx[e.Repo]
		if !ok {
			i = len(out)
			idx[e.Repo] = i
			out = append(out, watchedRepo{Repo: e.Repo, Entries: []ghEntry{}, Seen: []string{}})
		}
		out[i].Entries = append(out[i].Entries, e)
	}
	lines := strings.Split(readFile(filepath.Join(d.state, "awf-gh", "seen.jsonl"), 1<<20), "\n")
	for _, s := range dedupeSeen(lines) {
		if i, ok := idx[s.Repo]; ok {
			out[i].Seen = append(out[i].Seen, s.PR)
		}
	}
	writeJSON(w, out)
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
	out := []logIndex{}
	for _, line := range strings.Split(readFile(d.indexFile(), 8<<20), "\n") {
		var e logIndex
		if json.Unmarshal([]byte(line), &e) == nil && e.ID != "" {
			out = append(out, e)
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
			Base string `json:"baseRefName"`
		}
		loadJSON(filepath.Join(dir, "meta.json"), &meta)
		diff := readFile(filepath.Join(dir, "diff.patch"), 1<<20)
		out.Reviews = append(out.Reviews, review{
			ID:        name,
			Title:     meta.Title,
			Author:    meta.Author.Login,
			Base:      meta.Base,
			HasReview: exists(filepath.Join(dir, "review.md")),
			DiffLines: strings.Count(diff, "\n"),
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
			Comments []json.RawMessage `json:"comments"`
		}
		loadJSON(filepath.Join(dir, "pr.json"), &pr)
		out.Replies = append(out.Replies, reply{
			ID:       name,
			Title:    pr.Title,
			URL:      pr.URL,
			Author:   pr.Author.Login,
			Branch:   pr.Branch,
			Comments: len(pr.Comments),
			HasReply: exists(filepath.Join(dir, "reply.md")),
			Pushed:   strings.TrimSpace(readFile(filepath.Join(dir, "pushed.sha"), 100)),
		})
	}
	lines := strings.Split(readFile(filepath.Join(d.state, "awf-gh", "seen.jsonl"), 1<<20), "\n")
	out.Seen = dedupeSeen(lines)
	writeJSON(w, out)
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
