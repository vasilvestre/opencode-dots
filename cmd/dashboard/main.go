// Command dashboard serves a loopback page for what opencode-dots does: awf
// runs, PR review and reply artifacts, and the watcher's seen list.
//
// It also proxies /awf/* to the awf API so the page stays same-origin and needs
// no CORS. It binds 127.0.0.1 only and reads state/ with no writes.
package main

import (
	"encoding/json"
	"flag"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
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

	d := &dash{state: *state, awf: *awf, awfgh: *awfgh, active: map[string]bool{}, tried: map[string]bool{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		serveFile(w, r, *ui, "text/html; charset=utf-8")
	})
	mux.HandleFunc("/api/artifacts", d.artifacts)
	mux.HandleFunc("/api/file", d.file)
	mux.HandleFunc("/api/log", d.log)
	mux.HandleFunc("/api/repos", d.repos)
	mux.HandleFunc("/api/memory", d.tendMemory)
	mux.Handle("/awf/", http.StripPrefix("/awf", proxy))

	go d.watchRuns()

	log.Printf("dots dashboard on http://%s (awf at %s)", *addr, *awf)
	log.Fatal(http.ListenAndServe(*addr, mux))
}

type dash struct {
	state  string
	awf    string
	awfgh  string
	mu     sync.Mutex
	active map[string]bool
	tried  map[string]bool
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
	for _, e := range entries {
		info, err := e.Info()
		if err == nil && info.ModTime().Before(cutoff) {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// recordRuns records every execution awf knows and has no log for yet. A run
// can be shorter than the poll interval, so a terminal run is fetched once
// too: awf keeps its session for a while after the end.
func (d *dash) recordRuns() {
	var list struct {
		Body struct {
			Executions []struct {
				ID     string `json:"execution_id"`
				Status string `json:"status"`
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
			continue
		}
		d.mu.Lock()
		if d.active[e.ID] || d.tried[e.ID] {
			d.mu.Unlock()
			continue
		}
		if e.Status == "running" {
			d.active[e.ID] = true
			go d.record(e.ID, true)
		} else {
			d.tried[e.ID] = true
			go d.record(e.ID, false)
		}
		d.mu.Unlock()
	}
}

func (d *dash) record(id string, retry bool) {
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

func (d *dash) log(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if !logID(id) {
		http.NotFound(w, r)
		return
	}
	serveFile(w, r, filepath.Join(d.state, "logs", id+".jsonl"), "text/plain; charset=utf-8")
}

type memoryEntry struct {
	Repo    string `json:"repo"`
	Since   string `json:"since"`
	Lessons string `json:"lessons"`
	Memory  string `json:"memory"`
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
			}
			if e.Lessons == "" && e.Memory == "" {
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
