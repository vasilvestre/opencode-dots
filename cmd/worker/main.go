// Command worker is the dots-worker: it runs, on a machine of its own, the jobs a dots server
// queued for the repositories its token names (ADR 0042, docs/remote.md). It needs one outbound
// HTTPS connection to the server and the local awf serve; no tunnel and no open port.
//
// It follows the hub (a nudge when a job arrives) and polls the queue too, so a missed message
// delays a job and never loses it. It claims a job (the first claim wins), hands it to the local
// awf, renews the claim while it runs, and reports how it ended.
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

type job struct {
	ID       string         `json:"id"`
	Repo     string         `json:"repo"`
	Scope    string         `json:"scope"`
	Workflow string         `json:"workflow"`
	Inputs   map[string]any `json:"inputs"`
}

type worker struct {
	base       string // the server's worker entry, such as https://hub.example/worker
	token      string
	awf        string // the local awf serve
	dashboard  string // the local dashboard, asked what a run cost; empty skips the price
	client     *http.Client
	poll       time.Duration
	heartbeat  time.Duration
	awfPoll    time.Duration
	instance   string // tells this process apart from another one running the same token
	servedFile string // the repositories this worker serves, for the local dashboard; empty: not kept
	hintsFile  string // where the team's folder suggestions are kept for bin/where; empty: not kept
	whereBin   string // bin/where, to say where a missing project should go; empty: no advice
	wake       chan struct{}
	log        *log.Logger
}

func main() {
	home, _ := os.UserHomeDir()
	base := flag.String("url", os.Getenv("DOTS_WORKER_URL"), "the server's worker entry, such as https://hub.example/worker")
	tokenFile := flag.String("token-file", envOr("DOTS_WORKER_TOKEN_FILE", filepath.Join(home, ".config", "dots", "worker-token")), "file holding the worker token (dwt_...)")
	awf := flag.String("awf", envOr("DOTS_AWF_URL", "http://127.0.0.1:2511"), "the local awf serve")
	dash := flag.String("dashboard", envOr("DOTS_LOCAL_DASHBOARD", "http://127.0.0.1:8099"), "the local dashboard, asked what each run cost (empty: report no price)")
	poll := flag.Duration("poll", time.Minute, "how often the queue is read when the hub says nothing")
	hints := flag.String("hints-file", envOr("DOTS_HINTS", filepath.Join(home, ".config", "dots", "hints")), "where the team's folder suggestions are written, for bin/where (empty: not kept)")
	served := flag.String("served-file", envOr("DOTS_SERVED", filepath.Join(home, ".config", "dots", "served")), "where the repositories this worker serves are listed, for the local dashboard (empty: not kept)")
	once := flag.Bool("once", false, "claim and run what is pending, then stop")
	flag.Parse()
	if *base == "" {
		log.Fatal("worker: set -url or DOTS_WORKER_URL (the server's worker entry)")
	}
	token := strings.TrimSpace(os.Getenv("DOTS_WORKER_TOKEN"))
	if token == "" {
		raw, err := os.ReadFile(*tokenFile)
		if err != nil {
			log.Fatalf("worker: no token: set DOTS_WORKER_TOKEN or put it in %s", *tokenFile)
		}
		token = strings.TrimSpace(string(raw))
	}
	w := newWorker(*base, token, *awf)
	w.poll = *poll
	w.hintsFile = *hints
	w.servedFile = *served
	if exe, err := os.Executable(); err == nil {
		w.whereBin = filepath.Join(filepath.Dir(exe), "where")
	}
	w.dashboard = strings.TrimRight(*dash, "/")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *once {
		w.drain(ctx)
		return
	}
	w.run(ctx)
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func newWorker(base, token, awf string) *worker {
	raw := make([]byte, 6)
	rand.Read(raw)
	return &worker{instance: hex.EncodeToString(raw), base: strings.TrimRight(base, "/"), token: token, awf: strings.TrimRight(awf, "/"),
		client: &http.Client{Timeout: 30 * time.Second}, poll: time.Minute, heartbeat: 30 * time.Second, awfPoll: 5 * time.Second,
		wake: make(chan struct{}, 1), log: log.New(os.Stderr, "worker: ", log.LstdFlags)}
}

// call sends one request to the server's worker entry and decodes the JSON answer into out.
func (w *worker) call(ctx context.Context, method, path string, body any, out any) (int, error) {
	var rd io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, w.base+path, rd)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+w.token)
	req.Header.Set("X-Worker-Instance", w.instance)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := w.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if out != nil && resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(out); err != nil {
			return resp.StatusCode, err
		}
	} else {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	}
	return resp.StatusCode, nil
}

// run is the daemon: follow the hub in the background, and drain the queue on every nudge and every poll.
func (w *worker) run(ctx context.Context) {
	go w.follow(ctx)
	go w.keepHints(ctx)
	w.log.Printf("started, queue at %s, awf at %s", w.base, w.awf)
	t := time.NewTicker(w.poll)
	defer t.Stop()
	for {
		w.drain(ctx)
		select {
		case <-ctx.Done():
			return
		case <-w.wake:
		case <-t.C:
		}
	}
}

// drain claims and runs every pending job, one at a time.
func (w *worker) drain(ctx context.Context) {
	for ctx.Err() == nil {
		var list struct{ Jobs []job }
		code, err := w.call(ctx, http.MethodGet, "/api/jobs", nil, &list)
		if err != nil || code != http.StatusOK {
			if code == http.StatusUnauthorized {
				w.log.Printf("the server refuses this token (revoked or expired): ask the owner for a new one")
			} else if ctx.Err() == nil {
				w.log.Printf("cannot read the queue (HTTP %d): %v", code, err)
			}
			return
		}
		ran := false
		for _, j := range list.Jobs {
			var claimed job
			code, err := w.call(ctx, http.MethodPost, "/api/jobs/"+url.PathEscape(j.ID)+"/claim", nil, &claimed)
			if err != nil || code != http.StatusOK {
				continue // another worker was first, or the job is gone
			}
			w.execute(ctx, claimed)
			ran = true
			break // read the queue again: it may have changed while this ran
		}
		if !ran {
			return
		}
	}
}

// execute hands a claimed job to the local awf, keeps the claim alive, and reports the end.
func (w *worker) execute(ctx context.Context, j job) {
	w.log.Printf("took %s: %s/%s for %s", j.ID, j.Scope, j.Workflow, j.Repo)
	w.adviseIfMissing(ctx, j.Repo)
	status, note, execID := "failed", "", ""
	defer func() {
		// Report even when the worker is stopping: the server must hear that it is not coming back.
		fin, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		body := map[string]any{"status": status, "note": note, "cost_usd": w.runPrice(fin, execID)}
		if _, err := w.call(fin, http.MethodPost, "/api/jobs/"+url.PathEscape(j.ID)+"/finish", body, nil); err != nil {
			w.log.Printf("could not report %s: %v", j.ID, err)
		}
		w.log.Printf("%s ended %s %s", j.ID, status, note)
	}()
	id, err := w.startRun(ctx, j)
	if err != nil {
		note = err.Error()
		return
	}
	execID = id
	hb := time.NewTicker(w.heartbeat)
	defer hb.Stop()
	poll := time.NewTicker(w.awfPoll)
	defer poll.Stop()
	for {
		select {
		case <-ctx.Done():
			w.cancelRun(context.WithoutCancel(ctx), id)
			note = "the worker was stopped"
			return
		case <-hb.C:
			var r struct{ Cancelled bool }
			code, err := w.call(ctx, http.MethodPost, "/api/jobs/"+url.PathEscape(j.ID)+"/heartbeat", nil, &r)
			if err == nil && (code == http.StatusConflict || code == http.StatusNotFound) {
				w.cancelRun(ctx, id) // the server gave the job to someone else
				note, status = "the claim was lost", "failed"
				return
			}
			if err == nil && r.Cancelled {
				w.cancelRun(ctx, id)
				note = "cancelled by the watcher"
				return
			}
		case <-poll.C:
			st, err := w.runStatus(ctx, id)
			switch {
			case err != nil:
				w.log.Printf("awf does not answer for %s: %v", id, err)
			case st == "completed":
				status = "done"
				return
			case st == "running" || st == "pending":
			default:
				note = "awf ended " + st
				return
			}
		}
	}
}

func (w *worker) startRun(ctx context.Context, j job) (string, error) {
	raw, _ := json.Marshal(map[string]any{"inputs": j.Inputs})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s/api/workflows/%s/%s/run", w.awf, url.PathEscape(j.Scope), url.PathEscape(j.Workflow)), bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	resp, err := w.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("local awf unreachable: %w", err)
	}
	defer resp.Body.Close()
	var out struct {
		Body struct {
			ID string `json:"execution_id"`
		} `json:"body"`
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode/100 != 2 || json.Unmarshal(body, &out) != nil || out.Body.ID == "" {
		return "", fmt.Errorf("local awf refused the run (HTTP %d)", resp.StatusCode)
	}
	return out.Body.ID, nil
}

func (w *worker) runStatus(ctx context.Context, id string) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, w.awf+"/api/executions/"+url.PathEscape(id), nil)
	resp, err := w.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct{ Body struct{ Status string } }
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return "", err
	}
	if out.Body.Status == "" {
		return "", errors.New("awf lost the execution")
	}
	return out.Body.Status, nil
}

// runPrice is what the run spent, read from the local dashboard, which prices the run's own log.
// A missing dashboard or run gives 0: the server then shows the run as unpriced, never as free.
func (w *worker) runPrice(ctx context.Context, execID string) float64 {
	if w.dashboard == "" || execID == "" {
		return 0
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, w.dashboard+"/api/cost?id="+url.QueryEscape(execID), nil)
	resp, err := w.client.Do(req)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	var out struct {
		Cost float64 `json:"cost"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&out) != nil {
		return 0
	}
	return out.Cost
}

func (w *worker) cancelRun(ctx context.Context, id string) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodDelete, w.awf+"/api/executions/"+url.PathEscape(id), nil)
	if resp, err := w.client.Do(req); err == nil {
		resp.Body.Close()
	}
}

// follow keeps a subscription to the hub open for the topics of the token, and wakes the drain
// loop on every message. Its own token is fetched again before it ends: the hub cuts a stream at
// the token's expiry, so the stream is reopened at two thirds of the life, with Last-Event-ID so
// nothing in between is lost.
func (w *worker) follow(ctx context.Context) {
	last, backoff := "", 2*time.Second
	for ctx.Err() == nil {
		var ht struct {
			Hub       string   `json:"hub"`
			Token     string   `json:"token"`
			Topics    []string `json:"topics"`
			ExpiresAt string   `json:"expires_at"`
		}
		code, err := w.call(ctx, http.MethodGet, "/api/hub-token", nil, &ht)
		if err != nil || code != http.StatusOK || len(ht.Topics) == 0 {
			if ctx.Err() == nil {
				w.log.Printf("no hub token (HTTP %d): the queue is still polled", code)
			}
			sleep(ctx, backoff)
			backoff = min(backoff*2, time.Minute)
			continue
		}
		backoff = 2 * time.Second
		w.keepServed(ht.Topics)
		life := 10 * time.Minute
		if exp, err := time.Parse(time.RFC3339, ht.ExpiresAt); err == nil {
			life = time.Until(exp) * 2 / 3
		}
		sctx, cancel := context.WithTimeout(ctx, max(life, 30*time.Second))
		last = w.stream(sctx, ht.Hub, ht.Token, ht.Topics, last)
		cancel()
		sleep(ctx, time.Second)
	}
}

// stream reads one subscription until it ends and returns the last event id it saw.
func (w *worker) stream(ctx context.Context, hub, token string, topics []string, last string) string {
	q := url.Values{}
	for _, t := range topics {
		q.Add("match", t)
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, hub+"?"+q.Encode(), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "text/event-stream")
	if last != "" {
		req.Header.Set("Last-Event-ID", last)
	}
	c := &http.Client{} // no overall timeout: the stream lives until its context ends
	resp, err := c.Do(req)
	if err != nil {
		return last
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		w.log.Printf("the hub answered %d", resp.StatusCode)
		return last
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	got := false
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "id:"):
			last = strings.TrimSpace(strings.TrimPrefix(line, "id:"))
		case strings.HasPrefix(line, "data:"):
			got = true
		case line == "" && got:
			got = false
			select {
			case w.wake <- struct{}{}:
			default:
			}
		}
	}
	return last
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

// keepHints asks the server where the team keeps the projects of the owners this worker serves, now and
// every hour, and writes it in the format bin/where reads (`owner/*=folder`). The machine's own
// ~/.config/dots/paths always comes first: a hint is a suggestion, never an order.
func (w *worker) keepHints(ctx context.Context) {
	if w.hintsFile == "" {
		return
	}
	for ctx.Err() == nil {
		var out struct{ Hints map[string]string }
		if code, err := w.call(ctx, http.MethodGet, "/api/hints", nil, &out); err == nil && code == http.StatusOK {
			if err := writeHints(w.hintsFile, out.Hints); err != nil {
				w.log.Printf("cannot keep the team's folder suggestions: %v", err)
			}
		}
		sleep(ctx, time.Hour)
	}
}

// writeHints replaces the file atomically with one `owner/*=folder` line per suggestion, sorted.
func writeHints(path string, hints map[string]string) error {
	names := make([]string, 0, len(hints))
	for owner := range hints {
		names = append(names, owner)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString("# Where the team keeps its projects (suggested by the server; ~/.config/dots/paths comes first).\n")
	for _, owner := range names {
		fmt.Fprintf(&b, "%s/*=%s\n", owner, hints[owner])
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// adviseIfMissing says in the log where a project could live when it is not on this machine. The run goes on
// with a private clone, as before: nothing waits for the developer.
func (w *worker) adviseIfMissing(ctx context.Context, repo string) {
	if w.whereBin == "" {
		return
	}
	if _, err := os.Stat(w.whereBin); err != nil {
		return
	}
	if exec.CommandContext(ctx, w.whereBin, repo).Run() == nil {
		return // found
	}
	out, err := exec.CommandContext(ctx, w.whereBin, "--suggest", repo).Output()
	if dir := strings.TrimSpace(string(out)); err == nil && dir != "" {
		w.log.Printf("%s is not cloned on this machine. Suggested place: %s (git clone https://github.com/%s.git %s). It runs on a private clone meanwhile.", repo, dir, repo, dir)
		return
	}
	w.log.Printf("%s is not cloned on this machine. Say where your projects live in ~/.config/dots/paths (docs/paths.md); it runs on a private clone meanwhile.", repo)
}

// keepServed lists the repositories of the token's topics (`dots/repo/<owner>/<repo>`), one per line, so the
// local dashboard can show which projects this machine works on and whether each is cloned here.
func (w *worker) keepServed(topics []string) {
	if w.servedFile == "" {
		return
	}
	var repos []string
	for _, t := range topics {
		if repo, ok := strings.CutPrefix(t, "dots/repo/"); ok && strings.Count(repo, "/") == 1 {
			repos = append(repos, repo)
		}
	}
	sort.Strings(repos)
	body := "# Repositories this worker serves, written by dots-worker.\n" + strings.Join(repos, "\n")
	if len(repos) > 0 {
		body += "\n"
	}
	if err := os.MkdirAll(filepath.Dir(w.servedFile), 0o755); err != nil {
		return
	}
	tmp := w.servedFile + ".tmp"
	if os.WriteFile(tmp, []byte(body), 0o644) == nil {
		_ = os.Rename(tmp, w.servedFile)
	}
}
