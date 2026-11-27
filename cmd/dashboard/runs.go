package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"opencode-dots/internal/runrecorder"
)

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
	for i := 0; i < 20 && !exists(d.rec.Path(id)) && d.transcriptPath(id) == ""; i++ {
		time.Sleep(100 * time.Millisecond)
	}
	source, transcript := d.transcriptPath(id), true
	if source == "" {
		source, transcript = d.rec.Path(id), false
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
		if !transcript || d.rec.Ended(id) {
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

func (d *dash) log(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if !logID(id) {
		http.NotFound(w, r)
		return
	}
	serveFile(w, r, d.rec.Path(id), "text/plain; charset=utf-8")
}

var (
	roundPRRE  = regexp.MustCompile(`^[0-9]+$`)
	roundShaRE = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
)

func (d *dash) logs(w http.ResponseWriter, r *http.Request) {
	// Each row carries its price and target so the history table needs no
	// second request.
	type row struct {
		runrecorder.Entry
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
	for _, e := range d.rec.Entries() {
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
		out = append(out, row{Entry: e, Cost: usd, Repo: t["repo"], PR: t["pr"], Issue: t["issue"], Rounds: rounds, Models: info.Models, Provider: info.Provider, Route: info.Route})
	}
	writeJSON(w, out)
}

// watchRuns records the runs and keeps the caches that follow them.
func (d *dash) watchRuns() {
	go func() {
		for {
			d.pollExecutors()
			time.Sleep(time.Second)
		}
	}()
	d.rec.Run(func() {
		d.cacheTargets()
		d.targets.prune(d.rec.Has)
	})
}

// notifyRun sends one Discord message for a finished run. It reads the config on every call and
// never stops the recorder: a failed post is a log line.
func (d *dash) notifyRun(id, workflow, status string) {
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

func logID(id string) bool { return runrecorder.ValidID(id) }
