package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// watchRuns records the event stream of every running execution into
// state/logs. awf forgets events once the execution leaves its session
// registry, so a recording has to start while the run is still running.
func (d *dash) watchRuns() {
	d.seedNotified()
	go func() {
		for {
			d.pollExecutors()
			time.Sleep(time.Second)
		}
	}()
	lastPrune := time.Time{}
	for {
		d.recordRuns()
		d.cacheTargets()
		if time.Since(lastPrune) > 6*time.Hour {
			d.pruneLogs()
			lastPrune = time.Now()
		}
		time.Sleep(time.Second)
	}
}

// seedNotified marks the logs already on disk as announced. Without it a
// restart would send one message per recent run to the webhook.
func (d *dash) seedNotified() {
	entries, _ := os.ReadDir(filepath.Join(d.state, "logs"))
	seen := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() || e.Name() == "index.jsonl" || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		seen[strings.TrimSuffix(e.Name(), ".jsonl")] = true
	}
	d.mu.Lock()
	d.notified = seen
	d.mu.Unlock()
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
	for line := range strings.SplitSeq(readFile(filepath.Join(dir, "index.jsonl"), 8<<20), "\n") {
		var e logIndex
		if json.Unmarshal([]byte(line), &e) != nil || !live[e.ID+".jsonl"] {
			continue
		}
		keep.WriteString(line + "\n")
		indexed[e.ID] = true
	}
	_ = os.WriteFile(filepath.Join(dir, "index.jsonl"), []byte(keep.String()), 0o644)
	d.mu.Lock()
	d.indexed = indexed
	for id := range d.notified {
		if !live[id+".jsonl"] {
			delete(d.notified, id)
		}
	}
	d.mu.Unlock()

	d.targets.prune(func(id string) bool { return indexed[id] })
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
			d.notifyTerminal(e.ID, e.WorkflowName)
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
				var frame string
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

// recordedTerminal reports whether the recorder already saw the end of the run.
func (d *dash) recordedTerminal(id string) bool {
	return strings.Contains(d.logTail(id), `"kind":"workflow.`)
}

// logTail reads the end of the recorder's file: terminal events are written
// last, and a run log can grow beyond the page's read limit.
func (d *dash) logTail(id string) string {
	b, err := os.ReadFile(filepath.Join(d.state, "logs", id+".jsonl"))
	if err != nil {
		return ""
	}
	if len(b) > 4096 {
		b = b[len(b)-4096:]
	}
	return string(b)
}

// terminalStatus returns the terminal state the recorder saw for an execution.
func (d *dash) terminalStatus(id string) string {
	tail := d.logTail(id)
	for _, status := range []string{"completed", "failed"} {
		if strings.Contains(tail, `"kind":"workflow.`+status+`"`) {
			return status
		}
	}
	return ""
}

// notifyTerminal sends one Discord message for a finished run. It reads the
// config on every call and never stops the recorder: a failed post is a log
// line, and each execution id is considered once per process.
func (d *dash) notifyTerminal(id, workflow string) {
	d.mu.Lock()
	if d.notified == nil {
		d.notified = map[string]bool{}
	}
	seen := d.notified[id]
	d.mu.Unlock()
	if seen {
		return
	}

	status := d.terminalStatus(id)
	if status == "" {
		return
	}
	d.mu.Lock()
	if d.notified[id] {
		d.mu.Unlock()
		return
	}
	d.notified[id] = true
	d.mu.Unlock()

	cfg := readNotificationConfig(d.root)
	if !cfg.Discord.Enabled || cfg.Discord.Webhook == "" {
		return
	}
	post := d.discordPost
	if post == nil {
		post = postDiscord
	}
	if err := post(cfg.Discord.Webhook, fmt.Sprintf("dots: %s %s", workflow, status)); err != nil {
		log.Printf("notify %s: %v", id, err)
	}
}

// logID keeps the id from reaching the filesystem as a path.
func logID(id string) bool {
	if len(id) < 8 || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' {
			return false
		}
	}
	return true
}

func (d *dash) indexEntry(id string) (logIndex, bool) {
	for line := range strings.SplitSeq(readFile(d.indexFile(), 8<<20), "\n") {
		var e logIndex
		if json.Unmarshal([]byte(line), &e) == nil && e.ID == id {
			return e, true
		}
	}
	return logIndex{}, false
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
	for line := range strings.SplitSeq(readFile(d.indexFile(), 8<<20), "\n") {
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
	_, _ = f.Write(append(line, '\n'))
}

var (
	roundPRRE  = regexp.MustCompile(`^[0-9]+$`)
	roundShaRE = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
)

func (d *dash) logs(w http.ResponseWriter, r *http.Request) {
	// Each row carries its price and target so the history table needs no
	// second request.
	type row struct {
		logIndex
		Cost   float64           `json:"cost,omitempty"`
		Repo   string            `json:"repo,omitempty"`
		PR     string            `json:"pr,omitempty"`
		Issue  string            `json:"issue,omitempty"`
		Rounds []string          `json:"rounds,omitempty"`
		Models map[string]string `json:"models,omitempty"`
		// Provider is the agent CLI (opencode or claude); Route is the hidden
		// workflow fix-issue picked (owned or external).
		Provider string `json:"provider,omitempty"`
		Route    string `json:"route,omitempty"`
	}
	out := []row{}
	children := d.childStates()
	roundOwner := map[string]int{}
	for line := range strings.SplitSeq(readFile(d.indexFile(), 8<<20), "\n") {
		var e logIndex
		if json.Unmarshal([]byte(line), &e) == nil && e.ID != "" {
			usd, _ := d.runCost(e.ID)
			t := d.target(e.ID)
			var rounds []string
			if pr, sha := t["pr"], t["sha"]; e.WorkflowName == "review-pr" && roundPRRE.MatchString(pr) && roundShaRE.MatchString(sha) {
				matches, _ := filepath.Glob(filepath.Join(d.state, "reviews", "pr"+pr+"-"+sha, "diff.round*.patch"))
				for _, p := range matches {
					rel, err := filepath.Rel(d.state, p)
					if err != nil {
						continue
					}
					rounds = append(rounds, filepath.ToSlash(rel))
				}
				// A rerun wipes the directory, so only the latest review of this head owns the files.
				if prev, ok := roundOwner[pr+"-"+sha]; ok {
					out[prev].Rounds = nil
				}
				roundOwner[pr+"-"+sha] = len(out)
			}
			info := d.runInfo(e.ID, children)
			out = append(out, row{logIndex: e, Cost: usd, Repo: t["repo"], PR: t["pr"], Issue: t["issue"], Rounds: rounds, Models: info.Models, Provider: info.Provider, Route: info.Route})
		}
	}
	writeJSON(w, out)
}
