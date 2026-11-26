// Command dashboard serves a loopback page for what opencode-dots does: awf
// runs, PR review and reply artifacts, and the watcher's seen list.
//
// It also proxies /awf/* to the awf API so the page stays same-origin and needs
// no CORS. It binds 127.0.0.1 only. Writes are limited to state/logs/ and local
// config files (see /api/save and /api/notifications).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
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
	opencodeDB := flag.String("opencode-db", envOr("OPENCODE_DB", defaultOpencodeDB()), "opencode SQLite database, read-only, for the run view sessions")
	allowHost := flag.String("allow-host", envOr("DOTS_ALLOW_HOST", ""),
		"comma separated hosts allowed to write through the proxy, for the tailnet name")
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

	d := &dash{state: *state, root: *repoRoot, awf: *awf, awfgh: *awfgh, transcripts: *transcripts, opencode: *opencode, opencodeDB: *opencodeDB, allow: map[string]bool{}, active: map[string]bool{}, tried: map[string]bool{}, notified: map[string]bool{}}
	d.discordPost = postDiscord
	for _, h := range strings.Split(*allowHost, ",") {
		if h = strings.TrimSpace(h); h != "" {
			d.allow[h] = true
		}
	}
	d.targets = map[string]targetMemo{}
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
	mux.HandleFunc("/api/sessions", d.sessions)
	mux.HandleFunc("/api/logs", d.logs)
	mux.HandleFunc("/api/repos", d.repos)
	mux.HandleFunc("/api/repos/names", d.repoNames)
	mux.HandleFunc("/api/repos/config", d.reposConfig)
	mux.HandleFunc("/api/replay", d.replay)
	mux.HandleFunc("/api/thread", d.thread)
	mux.HandleFunc("/api/ask", d.ask)
	mux.HandleFunc("/api/ask/answer", d.askAnswer)
	mux.HandleFunc("/api/memory", d.tendMemory)
	mux.HandleFunc("/api/files", d.files)
	mux.HandleFunc("/api/models", d.models)
	mux.HandleFunc("/api/claims", d.claims)
	mux.HandleFunc("/api/notifications", d.notifications)
	mux.HandleFunc("/api/source", d.source)
	mux.HandleFunc("/api/save", d.saveFile)
	mux.Handle("/awf/", http.StripPrefix("/awf", proxy))

	go d.watchRuns()

	log.Printf("dots dashboard on http://%s (awf at %s)", *addr, *awf)
	log.Fatal(http.ListenAndServe(*addr, mux))
}

type dash struct {
	state        string
	root         string
	awf          string
	awfgh        string
	transcripts  string
	opencode     string
	opencodeDB   string          // opencode SQLite database, read-only
	allow        map[string]bool // extra hosts allowed to write, see -allow-host
	editMu       sync.Mutex
	modelCatalog func(context.Context) []string
	discordPost  func(webhook, message string) error
	mu           sync.Mutex
	active       map[string]bool
	tried        map[string]bool
	notified     map[string]bool // one completion message per execution id
	indexed      map[string]bool

	// The page asks for a price on every refresh; the last answer per run file
	// is reused until the file changes.
	costMu sync.Mutex
	costs  map[string]costMemo

	// replayInputs is a regexp pass over up to 4 MB of log, so the target per
	// run is parsed off the request path and kept by log version.
	targetMu sync.Mutex
	targets  map[string]targetMemo
}
