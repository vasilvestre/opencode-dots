package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

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

// targetMemo keeps the last answer for one run log, keyed by its size and
// modification time, because the page asks again on every refresh.
type targetMemo struct {
	size int64
	mod  time.Time
	in   map[string]string
}

// target returns the target cached for a run. cacheTargets does the parsing,
// so a request never reads a transcript.
func (d *dash) target(id string) map[string]string {
	d.targetMu.Lock()
	defer d.targetMu.Unlock()
	return d.targets[id].in
}

// completeTarget reports whether a parsed target names the repository and the
// pull request or issue, so a log that keeps growing cannot improve it.
func completeTarget(in map[string]string) bool {
	return in["repo"] != "" && (in["pr"] != "" || in["issue"] != "")
}

// cacheTargets parses the target of every indexed run whose log changed since
// the last pass. A log stops changing once its run ends, cancelled or not, so
// it is read once however it ended. A partial guess does not settle: a run
// that names its issue before its repository is read again when the log grows.
func (d *dash) cacheTargets() {
	d.mu.Lock()
	ids := make([]string, 0, len(d.indexed))
	for id := range d.indexed {
		ids = append(ids, id)
	}
	d.mu.Unlock()
	for _, id := range ids {
		path := d.transcriptPath(id)
		if path == "" {
			path = filepath.Join(d.state, "logs", id+".jsonl")
		}
		st, err := os.Stat(path)
		if err != nil {
			continue
		}
		d.targetMu.Lock()
		memo := d.targets[id]
		d.targetMu.Unlock()
		if completeTarget(memo.in) || (memo.size == st.Size() && memo.mod.Equal(st.ModTime())) {
			continue
		}
		in := replayInputs(readFile(path, 4<<20))
		d.targetMu.Lock()
		d.targets[id] = targetMemo{size: st.Size(), mod: st.ModTime(), in: in}
		d.targetMu.Unlock()
	}
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
