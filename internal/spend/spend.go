// Package spend reads what the dot's runs cost, from the same place the dashboard
// does (ADR 0008): the run index and each run's own log, where awf writes the
// provider price of an agent step as `cost_usd`. The dashboard shows it per run and
// per pull request; bin/budget (ADR 0036) sums it per repository for one UTC day.
package spend

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var (
	costUSDRE   = regexp.MustCompile(`"cost_usd":\s*([0-9.eE+-]+)`)
	repoIssueRE = regexp.MustCompile(`([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)#([0-9]+)`)
	prShaRE     = regexp.MustCompile(`\bpr([0-9]+)-([0-9a-f]{7,40})\b`)
	namedRepoRE = regexp.MustCompile(`\b([A-Za-z0-9][A-Za-z0-9_.-]*/[A-Za-z0-9][A-Za-z0-9_.-]*)`)
	issueRE     = regexp.MustCompile(`\bissue #([0-9]+)`)

	// What a workflow's own prompt template prints about its repository: a labelled
	// first line, or the opening sentence of tend-repo. Both are read from the head of the
	// first user message only, never from the text a pull request could carry.
	labelRepoRE  = regexp.MustCompile(`^\s*repo:[ \t]*"?([A-Za-z0-9][A-Za-z0-9_.-]*/[A-Za-z0-9][A-Za-z0-9_.-]*)"?[ \t]*(?:\n|$)`)
	tendOpenerRE = regexp.MustCompile(`^\s*You keep one repository honest\. It is ([A-Za-z0-9][A-Za-z0-9_.-]*/[A-Za-z0-9][A-Za-z0-9_.-]*),`)
	repoInputRE  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*/[A-Za-z0-9][A-Za-z0-9_.-]*$`)
)

// promptHead is how much of the first user message may name the repository.
const promptHead = 1500

// RepoInput returns the repository a workflow input holds, "" when it is not an
// owner/name.
func RepoInput(v string) string {
	v = strings.TrimSpace(v)
	if repoInputRE.MatchString(v) {
		return v
	}
	return ""
}

// SetRepo makes repo the repository of a run's inputs. The repository comes from the
// workflow itself (an input, or a line its own template prints), so it beats a guess
// from free text; and when that guess named another repository, the pull request or
// issue read along with it belonged to that one and is dropped.
func SetRepo(in map[string]string, repo string) {
	if old := in["repo"]; old != "" && !strings.EqualFold(old, repo) {
		delete(in, "pr")
		delete(in, "issue")
		delete(in, "sha")
	}
	in["repo"] = repo
}

// PromptRepo reads the repository a workflow's own prompt template prints, from the
// first user message of a run log: a `repo: owner/name` first line, or tend-repo's
// opening sentence. A repository only mentioned later, or inside the text of a pull request
// or an issue, is data and is not read.
func PromptRepo(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "data:"))
		if !strings.Contains(line, `"message.user"`) {
			continue
		}
		var ev struct {
			Text    string `json:"text"`
			Prompt  string `json:"prompt"`
			Content string `json:"content"`
			Payload struct {
				Text    string `json:"text"`
				Prompt  string `json:"prompt"`
				Content string `json:"content"`
			} `json:"payload"`
		}
		if json.Unmarshal([]byte(line), &ev) != nil {
			return ""
		}
		msg := ""
		for _, c := range []string{ev.Text, ev.Prompt, ev.Content, ev.Payload.Text, ev.Payload.Prompt, ev.Payload.Content} {
			if c != "" {
				msg = c
				break
			}
		}
		if len(msg) > promptHead {
			msg = msg[:promptHead]
		}
		if m := tendOpenerRE.FindStringSubmatch(msg); m != nil {
			return m[1]
		}
		if m := labelRepoRE.FindStringSubmatch(msg); m != nil {
			return m[1]
		}
		return "" // only the first user message counts
	}
	return ""
}

// SumCost adds up the prices recorded in a run log and counts the priced steps. A
// run started by an awf that does not record prices sums to zero.
func SumCost(text string) (float64, int) {
	var usd float64
	var steps int
	for _, line := range strings.Split(text, "\n") {
		for _, m := range costUSDRE.FindAllStringSubmatch(line, -1) {
			var v float64
			if _, err := fmt.Sscanf(m[1], "%g", &v); err == nil && v > 0 {
				usd += v
				steps++
			}
		}
	}
	return usd, steps
}

// ReplayInputs reads the inputs back from a run log. Every workflow spells the
// target in its prompts ("owner/repo#123", "issue #7") and review runs also
// write the head commit as "pr123-<sha>", so the log holds what the run was
// given. It is a guess: the page shows it as one and the operator can correct it.
func ReplayInputs(text string) map[string]string {
	raw := text
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
	if r := PromptRepo(raw); r != "" {
		SetRepo(out, r)
	}
	return out
}

// TranscriptDirs are the places awf keeps run transcripts, most specific first.
func TranscriptDirs(state string) []string {
	dirs := []string{filepath.Join(state, "awf", "transcripts")}
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		dirs = append(dirs, filepath.Join(xdg, "awf", "transcripts"))
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		dirs = append(dirs, filepath.Join(home, ".local", "share", "awf", "transcripts"))
	}
	return dirs
}

// runText returns the log of a run: the transcript first, since only it holds the
// agent prompts, then the recorder file under state/logs.
func runText(state string, dirs []string, id string) string {
	for _, dir := range dirs {
		if t := readCapped(filepath.Join(dir, id+".jsonl")); t != "" {
			return t
		}
	}
	return readCapped(filepath.Join(state, "logs", id+".jsonl"))
}

func readCapped(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	buf := make([]byte, 8<<20)
	n, _ := f.Read(buf)
	return string(buf[:n])
}

// Day sums the price of every run that started on the given UTC day, per
// repository. The repository of a run is read from its log like the dashboard does
// (ReplayInputs), so a run whose log names none is counted under "". The run is
// dated by its start, not by each step. A missing state/logs directory is an error
// (a wrong STATE_DIR must not read as no spend); a missing index inside it is no
// run at all. skipped counts index lines whose start time could not be read.
func Day(state string, dirs []string, day time.Time) (perRepo map[string]float64, skipped int, err error) {
	logs := filepath.Join(state, "logs")
	if st, e := os.Stat(logs); e != nil || !st.IsDir() {
		return nil, 0, fmt.Errorf("no run logs under %s", logs)
	}
	raw, e := os.ReadFile(filepath.Join(logs, "index.jsonl"))
	if e != nil && !os.IsNotExist(e) {
		return nil, 0, e
	}
	want := day.UTC().Format("2006-01-02")
	perRepo = map[string]float64{}
	seen := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		var ent struct {
			ID      string `json:"execution_id"`
			Started string `json:"started_at"`
		}
		if strings.TrimSpace(line) == "" || json.Unmarshal([]byte(line), &ent) != nil || ent.ID == "" || seen[ent.ID] {
			continue
		}
		seen[ent.ID] = true
		at, e := time.Parse(time.RFC3339Nano, ent.Started)
		if e != nil {
			skipped++
			continue
		}
		if at.UTC().Format("2006-01-02") != want {
			continue
		}
		text := runText(state, dirs, ent.ID)
		usd, _ := SumCost(text)
		perRepo[ReplayInputs(text)["repo"]] += usd
	}
	return perRepo, skipped, nil
}
