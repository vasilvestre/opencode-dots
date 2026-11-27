// Package jobs is the queue between a server that watches GitHub and the workers that run the
// work (ADR 0031, ADR 0042). The watcher posts a run the way it posts to awf; the queue keeps it
// until a worker claims it, so a worker that is off delays a job and never loses it. A claim is a
// lease the worker renews with a heartbeat: a worker that disappears gives the job back.
package jobs

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type State string

const (
	Pending   State = "pending"
	Taken     State = "taken"
	Done      State = "done"
	Failed    State = "failed"
	Cancelled State = "cancelled"
	Expired   State = "expired"
)

var (
	ErrNotFound  = errors.New("jobs: no such job")
	ErrTaken     = errors.New("jobs: already taken by another worker")
	ErrForbidden = errors.New("jobs: not a repository of this worker")
	ErrNotYours  = errors.New("jobs: this job is held by another worker")
)

// Job is one run to do.
type Job struct {
	ID       string         `json:"id"`
	Repo     string         `json:"repo"` // lower-cased owner/name, from the run's `repo` input
	Scope    string         `json:"scope"`
	Workflow string         `json:"workflow"`
	Inputs   map[string]any `json:"inputs"`
	Created  time.Time      `json:"created"`
	State    State          `json:"state"`
	By       string         `json:"by,omitempty"`
	Lease    time.Time      `json:"lease,omitzero"` // a taken job whose lease has passed is claimable again
	Finished time.Time      `json:"finished,omitzero"`
	Note     string         `json:"note,omitempty"`
}

// Queue holds the jobs, in memory and in a private file.
type Queue struct {
	mu   sync.Mutex
	path string
	Now  func() time.Time
	// Lease is how long a claim lives without a heartbeat. PendingTTL is how long an unclaimed job
	// waits before it expires (a worker that comes back after days must not run stale work).
	Lease, PendingTTL, Keep time.Duration
	jobs                    map[string]*Job
}

// Open loads the queue from stateDir (empty: memory only). A missing or unreadable file starts empty.
func Open(stateDir string) *Queue {
	q := &Queue{Now: time.Now, Lease: 2 * time.Minute, PendingTTL: 24 * time.Hour, Keep: 7 * 24 * time.Hour, jobs: map[string]*Job{}}
	if stateDir == "" {
		return q
	}
	q.path = filepath.Join(stateDir, "jobs.json")
	q.load()
	return q
}

// load reads the file once. One that is missing, unreadable, corrupted or readable by others
// leaves the queue empty.
func (q *Queue) load() {
	info, err := os.Stat(q.path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&^0o600 != 0 {
		return
	}
	raw, err := os.ReadFile(q.path)
	if err != nil {
		return
	}
	var f struct {
		Jobs map[string]*Job `json:"jobs"`
	}
	if json.Unmarshal(raw, &f) == nil && f.Jobs != nil {
		q.jobs = f.Jobs
	}
}

// find is the job with that id. The caller holds q.mu.
func (q *Queue) find(id string) (*Job, error) {
	j, ok := q.jobs[id]
	if !ok {
		return nil, ErrNotFound
	}
	return j, nil
}

// held is the job when by holds it (a taken one, or one cancelled under them). The caller holds q.mu.
func (q *Queue) held(id, by string, states ...State) (*Job, error) {
	j, err := q.find(id)
	if err != nil {
		return nil, err
	}
	if j.By != by {
		return nil, ErrNotYours
	}
	for _, s := range states {
		if j.State == s {
			return j, nil
		}
	}
	return nil, ErrNotYours
}

func (q *Queue) save() {
	if q.path == "" {
		return
	}
	raw, err := json.MarshalIndent(map[string]any{"version": 1, "jobs": q.jobs}, "", "  ")
	if err != nil {
		return
	}
	tmp := q.path + ".tmp"
	if os.WriteFile(tmp, raw, 0o600) == nil {
		_ = os.Rename(tmp, q.path)
	}
}

// settle moves what time has overtaken: an old pending job expires, an old finished one goes. The
// caller holds q.mu.
func (q *Queue) settle() {
	now := q.Now()
	for id, j := range q.jobs {
		switch {
		case j.State == Pending && now.Sub(j.Created) > q.PendingTTL:
			j.State, j.Finished, j.Note = Expired, now, "no worker took it in time"
		case j.State != Pending && j.State != Taken && now.Sub(j.Finished) > q.Keep:
			delete(q.jobs, id)
		}
	}
}

// Enqueue stores a run. The repository comes from the `repo` input: a run without one cannot be
// scoped to a worker, so it is refused.
func (q *Queue) Enqueue(scope, workflow string, inputs map[string]any) (Job, error) {
	repo, _ := inputs["repo"].(string)
	repo = strings.ToLower(strings.TrimSpace(repo))
	if !strings.Contains(repo, "/") || scope == "" || workflow == "" {
		return Job{}, errors.New("jobs: a run needs a scope, a workflow and a repo input")
	}
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return Job{}, err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.settle()
	j := &Job{ID: "job-" + hex.EncodeToString(raw), Repo: repo, Scope: scope, Workflow: workflow, Inputs: inputs, Created: q.Now(), State: Pending}
	q.jobs[j.ID] = j
	q.save()
	return *j, nil
}

func (q *Queue) claimable(j *Job) bool {
	return j.State == Pending || (j.State == Taken && !q.Now().Before(j.Lease))
}

// Pending lists the jobs a worker may claim now, oldest first, for the repositories it may serve.
func (q *Queue) Pending(repos map[string]bool) []Job {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.settle()
	var out []Job
	for _, j := range q.jobs {
		if repos[j.Repo] && q.claimable(j) {
			out = append(out, *j)
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Created.Before(out[b].Created) })
	return out
}

// Claim gives the job to by, if it is still free and one of the worker's repositories. The first
// claim wins; a claim whose lease ran out can be taken over. The holder claiming again renews its
// lease (the heartbeat) and gets the job back, whose State is Cancelled when the watcher cancelled
// the run: the worker must then stop it.
func (q *Queue) Claim(id, by string, repos map[string]bool) (Job, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.settle()
	j, err := q.find(id)
	if err != nil {
		return Job{}, err
	}
	if !repos[j.Repo] {
		return Job{}, ErrForbidden
	}
	switch {
	case j.By == by && j.State == Cancelled:
		return *j, nil // the stop signal; nothing to renew
	case j.By == by && j.State == Taken, q.claimable(j):
		j.State, j.By, j.Lease, j.Note = Taken, by, q.Now().Add(q.Lease), ""
		q.save()
		return *j, nil
	}
	return Job{}, ErrTaken
}

// Finish records the end of a claimed job: status is done or failed.
func (q *Queue) Finish(id, by string, status State, note string) (Job, error) {
	if status != Done && status != Failed {
		return Job{}, errors.New("jobs: a job finishes as done or failed")
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	j, err := q.held(id, by, Taken, Cancelled)
	if err != nil {
		return Job{}, err
	}
	if j.State == Taken {
		j.State = status
	}
	j.Finished, j.Note = q.Now(), note
	q.save()
	return *j, nil
}

// Cancel stops a job: a pending one never runs, a taken one tells its worker at the next heartbeat.
func (q *Queue) Cancel(id string) (Job, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	j, ok := q.jobs[id]
	if !ok || (j.State != Pending && j.State != Taken) {
		return Job{}, false
	}
	j.State, j.Finished, j.Note = Cancelled, q.Now(), "cancelled by the watcher"
	q.save()
	return *j, true
}

// All lists every job, newest first, for the owner's view.
func (q *Queue) All() []Job {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.settle()
	out := make([]Job, 0, len(q.jobs))
	for _, j := range q.jobs {
		out = append(out, *j)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Created.After(out[b].Created) })
	return out
}
