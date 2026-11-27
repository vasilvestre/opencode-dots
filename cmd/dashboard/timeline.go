package main

import (
	"fmt"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"opencode-dots/internal/spend"
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
	return spend.TranscriptDirs(d.state)
}

// runModels reports the model inputs awf recorded for a run. awf writes the
// inputs a run used to the sibling states/ directory of its transcripts, as
// <id>.json. Only model inputs are interesting here, and a run without a state
// file, or with no model inputs, has no models.
func (d *dash) runModels(id string) map[string]string {
	for _, dir := range d.transcriptDirs() {
		var state struct {
			Inputs map[string]string `json:"inputs"`
		}
		loadJSON(filepath.Join(filepath.Dir(dir), "states", id+".json"), &state)
		out := map[string]string{}
		for name, model := range state.Inputs {
			if model != "" && (name == "model" || strings.HasSuffix(name, "_model")) {
				out[name] = model
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return nil
}

// runState is the part of awf's per-run state file the page reads.
type runState struct {
	WorkflowName string            `json:"WorkflowName"`
	ParentRunID  string            `json:"ParentRunID"`
	Inputs       map[string]string `json:"Inputs"`
	States       map[string]struct {
		Output string `json:"Output"`
	} `json:"States"`
}

// childStates maps a run id to the run its call_workflow step started. awf keeps
// a sub-workflow as a run of its own, with the parent's id in its state file,
// and it is not listed as a running execution: the page reads its model through
// the parent. One pass over the state files, made once per request.
func (d *dash) childStates() map[string]runState {
	out := map[string]runState{}
	for _, dir := range d.transcriptDirs() {
		entries, err := os.ReadDir(filepath.Join(filepath.Dir(dir), "states"))
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			var st runState
			loadJSON(filepath.Join(filepath.Dir(dir), "states", e.Name()), &st)
			if st.ParentRunID != "" {
				out[st.ParentRunID] = st
			}
		}
	}
	return out
}

// runInfo is what the history and the running list show next to a run: its model
// inputs, the agent CLI it used and, for fix-issue, which hidden workflow the
// router picked.
type runInfo struct {
	Models   map[string]string
	Provider string
	Route    string
}

// runInfo reads them from the run's state file. A run with no model input of its
// own (the fix-issue router) takes the model of its child run. The provider is
// the run's provider input; a workflow without one always uses opencode.
func (d *dash) runInfo(id string, children map[string]runState) runInfo {
	info := runInfo{Models: d.runModels(id)}
	for _, dir := range d.transcriptDirs() {
		var st runState
		loadJSON(filepath.Join(filepath.Dir(dir), "states", id+".json"), &st)
		if st.WorkflowName == "" && st.Inputs == nil {
			continue
		}
		info.Provider = "opencode"
		if p := st.Inputs["provider"]; p == "claude" || p == "opencode" {
			info.Provider = p
		}
		if out := strings.ToLower(strings.TrimSpace(st.States["route"].Output)); st.WorkflowName == "fix-issue" && (out == "owned" || out == "external") {
			info.Route = out
		}
		break
	}
	if len(info.Models) == 0 {
		if child, ok := children[id]; ok {
			info.Models = map[string]string{}
			for name, model := range child.Inputs {
				if model != "" && (name == "model" || strings.HasSuffix(name, "_model")) {
					info.Models[name] = model
				}
			}
			if len(info.Models) == 0 {
				info.Models = nil
			}
		}
	}
	return info
}

// replayRun answers /api/replay: what a finished run was asked to do, so the
// page can offer the same run again. awf keeps no copy of the inputs, so the
// values come either from the run log (see spend.ReplayInputs) or from the page's own
// record written when it started the run (state/runs/<id>.json).
type replayRun struct {
	Workflow string            `json:"workflow"`
	Scope    string            `json:"scope"`
	Inputs   map[string]string `json:"inputs"`
	Source   string            `json:"source"`
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
		in := d.targetOf(id, readFile(path, 4<<20))
		d.targetMu.Lock()
		d.targets[id] = targetMemo{size: st.Size(), mod: st.ModTime(), in: in}
		d.targetMu.Unlock()
	}
}

var (
	workflowNameRE = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
)

// runText returns the log of a run: the transcript first, since only it holds
// the agent prompts, then the recorder file.
func (d *dash) runText(id string) string {
	if p := d.transcriptPath(id); p != "" {
		return readFile(p, 4<<20)
	}
	return readFile(filepath.Join(d.state, "logs", id+".jsonl"), 1<<20)
}

// targetOf is what spend.ReplayInputs reads from a run log, with the repository
// awf recorded as the run's own `repo` input when the log names none. Only that
// input and the template's own labelled line count; free text never does.
func (d *dash) targetOf(id, text string) map[string]string {
	in := spend.ReplayInputs(text)
	if in["repo"] != "" {
		return in
	}
	for _, dir := range d.transcriptDirs() {
		var st runState
		loadJSON(filepath.Join(filepath.Dir(dir), "states", id+".json"), &st)
		if r := spend.RepoInput(st.Inputs["repo"]); r != "" {
			in["repo"] = r
			break
		}
	}
	return in
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
	if j, ok := d.queuedJob(id); ok {
		// A run a worker did has no log here, but the queue kept what it was given.
		out.Workflow, out.Scope, out.Source = j.Workflow, j.Scope, "queue"
		for k, v := range j.Inputs {
			if str, isStr := v.(string); isStr {
				out.Inputs[k] = str
			} else {
				out.Inputs[k] = fmt.Sprint(v)
			}
		}
		writeJSON(w, out)
		return
	}
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
		if guess := spend.ReplayInputs(d.runText(id)); len(guess) > 0 {
			out.Inputs = guess
			out.Source = "log"
		}
	}
	writeJSON(w, out)
}
