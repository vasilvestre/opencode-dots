package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

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

var repoNameRE = regexp.MustCompile(`^[A-Za-z0-9_.-]*[A-Za-z0-9_-][A-Za-z0-9_.-]*/[A-Za-z0-9_.-]*[A-Za-z0-9_-][A-Za-z0-9_.-]*$`)

// repoNames suggests the repositories this machine already knows: the watcher
// config, the daily list, the path map and the directories a pass wrote under
// state/tend. The run dialog offers them, and still accepts any other name.
func (d *dash) repoNames(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, d.knownRepos())
}

// knownRepos is the sorted list behind repoNames. A visitor's view is built from it too.
func (d *dash) knownRepos() []string {
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
	slices.Sort(out)
	return out
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
