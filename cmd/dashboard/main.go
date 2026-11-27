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
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"opencode-dots/internal/auth"
	"opencode-dots/internal/jobs"
	"opencode-dots/internal/workertokens"
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
	ghClient := flag.String("github-client-id", envOr("DOTS_GITHUB_CLIENT_ID", ""),
		"GitHub OAuth app client id; sign-in for visitors is on only when it is set (docs/dashboard-auth.md)")
	ghURL := flag.String("github-url", envOr("DOTS_GITHUB_URL", "https://github.com"), "GitHub device flow host")
	ghAPI := flag.String("github-api", envOr("DOTS_GITHUB_API_URL", "https://api.github.com"), "GitHub REST API base URL")
	minPerm := flag.String("min-permission", envOr("DOTS_MIN_PERMISSION", "push"),
		"permission a visitor needs on a repository to see it: push (write or better) or pull (read or better)")
	teamAddr := flag.String("team-addr", envOr("DOTS_TEAM_ADDR", ""),
		"loopback address of a second listener where every request is a visitor, for SSH forwards (docs/team-ui.md)")
	hubURL := flag.String("hub-url", envOr("DOTS_HUB_URL", ""),
		"public endpoint of the Mercure hub; subscriber tokens are issued only when it is set (docs/remote.md)")
	hubAud := flag.String("hub-audience", envOr("DOTS_HUB_AUDIENCE", ""),
		"the hub endpoint as the hub itself sees a request (http behind a TLS proxy); empty means -hub-url")
	workerAddr := flag.String("worker-addr", envOr("DOTS_WORKER_ADDR", ""),
		"loopback address of a listener that serves only the worker token exchange (docs/remote.md)")
	hubInternal := flag.String("hub-internal", envOr("DOTS_HUB_INTERNAL", "http://127.0.0.1:3901/.well-known/mercure"),
		"the hub endpoint this process publishes to (loopback)")
	hubEnv := flag.String("hub-env", envOr("DOTS_HUB_ENV", defaultHubEnv()), "the hub's settings file holding its subscriber key")
	flag.Parse()
	if err := checkTeamAddr(*teamAddr, *addr, *ghClient != ""); err != nil {
		log.Fatal(err)
	}
	if _, err := auth.ParseMinPermission(*minPerm); err != nil {
		log.Fatal(err)
	}

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
	d.wtokens = workertokens.New(*state)
	d.queue = jobs.Open(*state)
	if d.hub, err = newHubIssuer(*hubURL, *hubAud, *hubInternal, *hubEnv); err != nil {
		log.Fatal(err)
	}
	d.auth = auth.New(auth.Config{ClientID: *ghClient, GitHubURL: *ghURL, APIURL: *ghAPI, MinPermission: *minPerm, StateDir: *state})
	d.execDash = parseExecutorDashboards(envOr("DOTS_EXECUTOR_DASHBOARDS", ""))
	for h := range strings.SplitSeq(*allowHost, ",") {
		if h = strings.TrimSpace(h); h != "" {
			d.allow[h] = true
		}
	}
	d.targets = map[string]targetMemo{}
	d.costs = map[string]costMemo{}
	d.indexed = d.loadIndexed()
	mux := routes(d, *ui, proxy)

	go d.watchRuns()
	if d.auth != nil {
		go d.auth.Sweep(10 * time.Minute)
	}

	if *teamAddr != "" {
		go func() {
			log.Printf("dots dashboard, team listener (visitors only) on http://%s", *teamAddr)
			log.Fatal(serve(*teamAddr, teamHandler(mux)))
		}()
	}

	if *workerAddr != "" {
		if err := checkWorkerAddr(*workerAddr, *addr, *teamAddr); err != nil {
			log.Fatal(err)
		}
		go func() {
			log.Printf("dots dashboard, worker token exchange on http://%s", *workerAddr)
			log.Fatal(serve(*workerAddr, d.workerHandler()))
		}()
	}

	log.Printf("dots dashboard on http://%s (awf at %s)", *addr, *awf)
	log.Fatal(serve(*addr, mux))
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
	hub          *hubIssuer      // nil: no subscriber tokens
	wtokens      *workertokens.Store
	queue        *jobs.Queue
	clones       cloneState
	gh           string // the gh binary that clones a project; empty means "gh" on the PATH
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

	// spend.ReplayInputs is a regexp pass over up to 4 MB of log, so the target per
	// run is parsed off the request path and kept by log version.
	targetMu sync.Mutex
	targets  map[string]targetMemo

	// Snapshots of the executors' awf (remote.go).
	remoteMu sync.Mutex
	remote   map[string]remoteSnapshot
	remoteAt time.Time
	execDash map[string]string // executor name -> its dashboard, see DOTS_EXECUTOR_DASHBOARDS

	// auth is nil unless a GitHub OAuth client id is configured: sign-in for visitors is off
	// and every request is the owner's (auth.go, visibility.go).
	auth *auth.Auth

	// clock is the time source of the Home page; nil means time.Now.
	clock func() time.Time
}

func defaultHubEnv() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "dots", "hub.env")
}

// serve is http.ListenAndServe with a header timeout, so a client that never finishes its request line cannot hold a connection.
func serve(addr string, h http.Handler) error {
	return (&http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 10 * time.Second}).ListenAndServe()
}
