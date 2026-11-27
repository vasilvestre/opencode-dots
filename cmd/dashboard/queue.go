package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"opencode-dots/internal/events"
	"opencode-dots/internal/jobs"
	"opencode-dots/internal/repos"
)

// The queue (internal/jobs) is an awf look-alike for a watcher: awf-gh posts a run to
// {awf_url}/api/workflows/{scope}/{workflow}/run and may cancel it with DELETE
// {awf_url}/api/executions/{id}. With awf_url set to the dashboard's /queue the run is kept until a
// worker claims it (worker listener, jobs routes below). Mercure only wakes a worker up: the
// queue is the truth, and a worker polls it too, so a missed message delays a job and loses none.

// publish tells the hub that something happened to a job, on the repository's private topic.
// Best effort and in the background: the queue does not depend on it.
func (d *dash) publish(j jobs.Job, kind, by, status string) {
	if d.hub == nil || d.hub.publisher == nil {
		return
	}
	payload, _ := json.Marshal(map[string]string{"kind": kind, "job": j.ID, "repo": j.Repo, "workflow": j.Workflow, "by": by, "status": status})
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := d.hub.publisher.Publish(ctx, events.RepoTopic(j.Repo), payload, true); err != nil {
			log.Printf("hub: could not publish a %s event: %v", kind, err)
		}
	}()
}

// queueRun accepts a run the way awf does: 202 and an execution id, here the job id.
func (d *dash) queueRun(w http.ResponseWriter, r *http.Request) {
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/queue/api/workflows/"), "/")
	parts := strings.Split(rest, "/")
	if r.Method != http.MethodPost || len(parts) != 3 || parts[2] != "run" {
		http.NotFound(w, r)
		return
	}
	var in struct {
		Inputs map[string]any `json:"inputs"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil || in.Inputs == nil {
		writeJSONStatus(w, http.StatusUnprocessableEntity, map[string]string{"error": "inputs are required"})
		return
	}
	j, err := d.queue.Enqueue(parts[0], parts[1], in.Inputs)
	if err != nil {
		writeJSONStatus(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	d.publish(j, "job", "", "pending")
	writeJSONStatus(w, http.StatusAccepted, map[string]any{"body": map[string]string{"execution_id": j.ID}})
}

// queueCancel is DELETE /queue/api/executions/{id}: 204 whether or not the job is known, as awf does.
func (d *dash) queueCancel(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/queue/api/executions/")
	if r.Method != http.MethodDelete || id == "" {
		http.NotFound(w, r)
		return
	}
	if j, ok := d.queue.Cancel(id); ok {
		d.publish(j, "cancelled", j.By, "cancelled")
	}
	w.WriteHeader(http.StatusNoContent)
}

// queueList is the owner's view of every job.
func (d *dash) queueList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"jobs": d.queue.All()})
}

// workerAuth resolves the bearer token of a worker and the repositories it may serve.
func (d *dash) workerAuth(w http.ResponseWriter, r *http.Request) (workerToken, map[string]bool, bool) {
	t, ok := d.wtokens.lookup(bearer(r))
	if !ok {
		time.Sleep(d.wtokens.failDelay)
		writeJSONStatus(w, http.StatusUnauthorized, map[string]string{"error": "invalid or revoked worker token"})
		return workerToken{}, nil, false
	}
	set := map[string]bool{}
	for _, repo := range d.workerRepos(t) {
		set[strings.ToLower(repo)] = true
	}
	return t, set, true
}

// workerName is who holds a claim: the token's label and this process's instance id, so two
// machines sharing one token are two holders and never renew each other's claim.
func workerName(t workerToken, r *http.Request) string {
	inst := strings.Map(func(c rune) rune {
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' {
			return c
		}
		return -1
	}, strings.ToLower(r.Header.Get("X-Worker-Instance")))
	if len(inst) > 32 {
		inst = inst[:32]
	}
	return t.Label + "#" + inst
}

func (d *dash) jobsPending(w http.ResponseWriter, r *http.Request) {
	_, repos, ok := d.workerAuth(w, r)
	if !ok {
		return
	}
	writeJSON(w, map[string]any{"jobs": d.queue.Pending(repos)})
}

func (d *dash) jobClaim(w http.ResponseWriter, r *http.Request) {
	t, repos, ok := d.workerAuth(w, r)
	if !ok {
		return
	}
	j, err := d.queue.Claim(r.PathValue("id"), workerName(t, r), repos)
	if err != nil {
		jobError(w, err)
		return
	}
	d.publish(j, "taken", workerName(t, r), "taken")
	writeJSON(w, j)
}

func (d *dash) jobHeartbeat(w http.ResponseWriter, r *http.Request) {
	t, repos, ok := d.workerAuth(w, r)
	if !ok {
		return
	}
	j, err := d.queue.Claim(r.PathValue("id"), workerName(t, r), repos)
	if err != nil {
		jobError(w, err)
		return
	}
	writeJSON(w, map[string]bool{"cancelled": j.State == jobs.Cancelled})
}

func (d *dash) jobFinish(w http.ResponseWriter, r *http.Request) {
	t, _, ok := d.workerAuth(w, r)
	if !ok {
		return
	}
	var in struct {
		Status string  `json:"status"`
		Note   string  `json:"note"`
		Cost   float64 `json:"cost_usd"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in) != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	j, err := d.queue.Finish(r.PathValue("id"), workerName(t, r), jobs.State(in.Status), in.Note, in.Cost)
	if err != nil {
		jobError(w, err)
		return
	}
	d.publish(j, "finished", workerName(t, r), in.Status)
	writeJSON(w, map[string]string{"status": "ok"})
}

func jobError(w http.ResponseWriter, err error) {
	code := http.StatusBadRequest
	switch {
	case errors.Is(err, jobs.ErrNotFound):
		code = http.StatusNotFound
	case errors.Is(err, jobs.ErrTaken), errors.Is(err, jobs.ErrNotYours):
		code = http.StatusConflict
	case errors.Is(err, jobs.ErrForbidden):
		code = http.StatusForbidden
	case errors.Is(err, jobs.ErrPaused):
		code = http.StatusLocked
	}
	writeJSONStatus(w, code, map[string]string{"error": err.Error()})
}

// awfStatus maps a job state to the status awf gives an execution, so the Activity view reads a
// queued run like any other.
func awfStatus(s jobs.State) string {
	switch s {
	case jobs.Pending:
		return "pending"
	case jobs.Taken:
		return "running"
	case jobs.Done:
		return "completed"
	case jobs.Failed, jobs.Expired:
		return "failed"
	}
	return string(s)
}

// jobTarget is the repository and pull request or issue a run is about, as the history shows it.
func jobTarget(j jobs.Job) string {
	for _, k := range []string{"pr", "issue"} {
		if n, _ := j.Inputs[k].(string); n != "" {
			return j.Repo + "#" + n
		}
	}
	return j.Repo
}

// queueExecutions answers like awf's /api/executions: the runs still waiting or running.
func (d *dash) queueExecutions(w http.ResponseWriter, r *http.Request) {
	list := []map[string]any{}
	for _, j := range d.queue.All() {
		if j.State == jobs.Pending || j.State == jobs.Taken {
			list = append(list, map[string]any{"execution_id": j.ID, "workflow_name": j.Workflow, "status": awfStatus(j.State),
				"started_at": j.Created.Format(time.RFC3339Nano), "updated_at": j.Created.Format(time.RFC3339Nano), "target": jobTarget(j), "worker": j.By, "replayable": true})
		}
	}
	writeJSON(w, map[string]any{"body": map[string]any{"executions": list}})
}

// queueHistory answers like awf's /api/history: the runs that ended, newest first.
func (d *dash) queueHistory(w http.ResponseWriter, r *http.Request) {
	list := []map[string]any{}
	for _, j := range d.queue.All() {
		if j.State == jobs.Pending || j.State == jobs.Taken {
			continue
		}
		e := map[string]any{"id": j.ID, "execution_id": j.ID, "workflow_name": j.Workflow, "status": awfStatus(j.State),
			"started_at": j.Created.Format(time.RFC3339Nano), "target": jobTarget(j), "worker": j.By, "note": j.Note, "cost": j.Cost, "replayable": true}
		if !j.Finished.IsZero() {
			e["completed_at"] = j.Finished.Format(time.RFC3339Nano)
			e["duration_ms"] = j.Finished.Sub(j.Created).Milliseconds()
		}
		list = append(list, e)
		if len(list) == 50 {
			break
		}
	}
	writeJSON(w, map[string]any{"body": map[string]any{"entries": list}})
}

// queueSpend is the finished jobs, which carry the price their worker reported. A server that
// queues nothing has none.
func (d *dash) queueSpend() []jobs.Job {
	if d.queue == nil {
		return nil
	}
	var out []jobs.Job
	for _, j := range d.queue.All() {
		if j.State == jobs.Done || j.State == jobs.Failed {
			out = append(out, j)
		}
	}
	return out
}

// queuedJob is the job with that id, when the queue knows it.
func (d *dash) queuedJob(id string) (jobs.Job, bool) {
	if d.queue == nil || !strings.HasPrefix(id, "job-") {
		return jobs.Job{}, false
	}
	for _, j := range d.queue.All() {
		if j.ID == id {
			return j, true
		}
	}
	return jobs.Job{}, false
}

// jobHints tells a worker where its team keeps the projects of the owners it serves
// (settings.checkout_hints). Only the owners of repositories the token covers: a worker learns nothing
// about the others. A suggestion, never an order.
func (d *dash) jobHints(w http.ResponseWriter, r *http.Request) {
	_, served, ok := d.workerAuth(w, r)
	if !ok {
		return
	}
	hints := map[string]string{}
	if doc, err := repos.Load(filepath.Join(d.root, "repos.yaml")); err == nil {
		for repo := range served {
			owner := repo[:strings.IndexByte(repo, '/')]
			if dir, ok := doc.Settings.CheckoutHint(owner); ok {
				hints[owner] = dir
			}
		}
	}
	writeJSON(w, map[string]any{"hints": hints})
}
