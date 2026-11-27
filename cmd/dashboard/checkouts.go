package main

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// The projects of this machine (docs/paths.md): which repositories it works on, whether each is cloned
// here, where the team suggests keeping it, and a button that clones it there. Owner only: it runs
// `gh repo clone` as the owner, into a folder that `bin/where --suggest` named and that does not exist.

const cloneTimeout = 15 * time.Minute

type checkoutRow struct {
	Repo      string `json:"repo"`
	State     string `json:"state"` // found, missing, cloning, failed
	Path      string `json:"path,omitempty"`
	Suggested string `json:"suggested,omitempty"`
	Error     string `json:"error,omitempty"`
}

type cloneState struct {
	mu   sync.Mutex
	runs map[string]*checkoutRow // by repository, while or after a clone
}

func (d *dash) whereBin() string { return filepath.Join(d.root, "bin", "where") }

// where runs bin/where for a repository: the checkout path, or the suggested folder with --suggest.
func (d *dash) where(ctx context.Context, repo string, suggest bool) string {
	args := []string{repo}
	if suggest {
		args = []string{"--suggest", repo}
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, d.whereBin(), args...)
	// The explicit map of this installation (the `checkout:` keys of repos.yaml): without it bin/where
	// would look for /work/repos.map, which only exists in the container.
	if os.Getenv("REPOS_MAP") == "" {
		cmd.Env = append(os.Environ(), "REPOS_MAP="+filepath.Join(d.root, "repos.map"))
	}
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// servedRepos are the repositories the local worker serves (the file it writes). Not the ones this
// installation's own configuration knows: those are the owner's own projects, found through repos.map,
// and listing them as "not cloned" would only be noise.
func (d *dash) servedRepos() []string {
	seen := map[string]bool{}
	home, _ := os.UserHomeDir()
	if f, err := os.Open(filepath.Join(home, ".config", "dots", "served")); err == nil {
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			if line := strings.TrimSpace(sc.Text()); repoNameRE.MatchString(line) {
				seen[line] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for r := range seen {
		out = append(out, r)
	}
	sort.Strings(out)
	return out
}

func (d *dash) checkoutRows(ctx context.Context) []checkoutRow {
	rows := []checkoutRow{}
	for _, repo := range d.servedRepos() {
		row := checkoutRow{Repo: repo}
		if p := d.where(ctx, repo, false); p != "" {
			row.State, row.Path = "found", p
		} else {
			row.State, row.Suggested = "missing", d.where(ctx, repo, true)
		}
		d.clones.mu.Lock()
		if c, ok := d.clones.runs[repo]; ok && row.State != "found" {
			row.State, row.Error = c.State, c.Error
		}
		d.clones.mu.Unlock()
		rows = append(rows, row)
	}
	return rows
}

func (d *dash) checkouts(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"projects": d.checkoutRows(r.Context())})
}

// checkoutClone clones a missing project into the folder the machine map or the team suggests.
func (d *dash) checkoutClone(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var in struct {
		Repo string `json:"repo"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&in) != nil || !repoNameRE.MatchString(in.Repo) {
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "repo is OWNER/REPO"})
		return
	}
	known := false
	for _, repo := range d.servedRepos() {
		known = known || repo == in.Repo
	}
	if !known {
		writeJSONStatus(w, http.StatusNotFound, map[string]string{"error": "this machine does not work on that repository"})
		return
	}
	if p := d.where(r.Context(), in.Repo, false); p != "" {
		writeJSONStatus(w, http.StatusConflict, map[string]string{"error": "already cloned at " + p})
		return
	}
	target := d.where(r.Context(), in.Repo, true)
	home, _ := os.UserHomeDir()
	switch {
	case target == "":
		writeJSONStatus(w, http.StatusUnprocessableEntity, map[string]string{"error": "no folder is suggested for this owner: add a line to ~/.config/dots/paths"})
		return
	case !filepath.IsAbs(target) || home == "" || !strings.HasPrefix(target, home+string(filepath.Separator)) || strings.Contains(target, ".."):
		writeJSONStatus(w, http.StatusUnprocessableEntity, map[string]string{"error": "the suggested folder must be inside your home directory"})
		return
	}
	if _, err := os.Stat(target); err == nil {
		writeJSONStatus(w, http.StatusConflict, map[string]string{"error": target + " already exists and is not a clone of " + in.Repo})
		return
	}
	d.clones.mu.Lock()
	if c, ok := d.clones.runs[in.Repo]; ok && c.State == "cloning" {
		d.clones.mu.Unlock()
		writeJSONStatus(w, http.StatusConflict, map[string]string{"error": "already cloning"})
		return
	}
	if d.clones.runs == nil {
		d.clones.runs = map[string]*checkoutRow{}
	}
	d.clones.runs[in.Repo] = &checkoutRow{Repo: in.Repo, State: "cloning", Suggested: target}
	d.clones.mu.Unlock()
	go d.runClone(in.Repo, target)
	writeJSONStatus(w, http.StatusAccepted, map[string]string{"status": "cloning", "target": target})
}

func (d *dash) runClone(repo, target string) {
	ctx, cancel := context.WithTimeout(context.Background(), cloneTimeout)
	defer cancel()
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err == nil {
		out, runErr := exec.CommandContext(ctx, d.ghBin(), "repo", "clone", repo, target).CombinedOutput()
		d.finishClone(repo, runErr, string(out))
		return
	}
	d.finishClone(repo, os.ErrPermission, "cannot create the folder")
}

func (d *dash) ghBin() string {
	if d.gh != "" {
		return d.gh
	}
	return "gh"
}

func (d *dash) finishClone(repo string, err error, out string) {
	d.clones.mu.Lock()
	defer d.clones.mu.Unlock()
	c := d.clones.runs[repo]
	if c == nil {
		return
	}
	if err != nil {
		c.State, c.Error = "failed", strings.TrimSpace(out)
		if len(c.Error) > 300 {
			c.Error = c.Error[len(c.Error)-300:]
		}
		return
	}
	delete(d.clones.runs, repo) // cloned: the next listing finds it
}
