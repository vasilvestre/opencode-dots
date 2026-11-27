package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"opencode-dots/internal/jobs"
)

const tok = "dwt_test"

// fakeServer is the worker listener of a dots server over the real queue.
type fakeServer struct {
	*httptest.Server
	q     *jobs.Queue
	repos map[string]bool
	hub   *httptest.Server
	nudge chan struct{}
}

func newFakeServer(t *testing.T) *fakeServer {
	f := &fakeServer{q: jobs.Open(""), repos: map[string]bool{"org/app": true}, nudge: make(chan struct{}, 4)}
	mux := http.NewServeMux()
	auth := func(h func(http.ResponseWriter, *http.Request)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer "+tok {
				w.WriteHeader(401)
				return
			}
			h(w, r)
		}
	}
	mux.HandleFunc("GET /api/jobs", auth(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"jobs": f.q.Pending(f.repos)})
	}))
	mux.HandleFunc("POST /api/jobs/{id}/claim", auth(func(w http.ResponseWriter, r *http.Request) {
		j, err := f.q.Claim(r.PathValue("id"), r.Header.Get("X-Worker-Instance"), f.repos)
		if err != nil {
			w.WriteHeader(409)
			return
		}
		json.NewEncoder(w).Encode(j)
	}))
	mux.HandleFunc("POST /api/jobs/{id}/heartbeat", auth(func(w http.ResponseWriter, r *http.Request) {
		j, err := f.q.Claim(r.PathValue("id"), r.Header.Get("X-Worker-Instance"), f.repos)
		if err != nil {
			w.WriteHeader(409)
			return
		}
		json.NewEncoder(w).Encode(map[string]bool{"cancelled": j.State == jobs.Cancelled})
	}))
	mux.HandleFunc("POST /api/jobs/{id}/finish", auth(func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Status, Note string }
		json.NewDecoder(r.Body).Decode(&in)
		if _, err := f.q.Finish(r.PathValue("id"), r.Header.Get("X-Worker-Instance"), jobs.State(in.Status), in.Note); err != nil {
			w.WriteHeader(409)
			return
		}
		w.Write([]byte(`{"status":"ok"}`))
	}))
	f.hub = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer hubtok" || len(r.URL.Query()["match"]) == 0 {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		select {
		case <-f.nudge:
			fmt.Fprint(w, "id: e1\ndata: {\"kind\":\"job\"}\n\n")
			w.(http.Flusher).Flush()
		case <-r.Context().Done():
			return
		}
		<-r.Context().Done()
	}))
	mux.HandleFunc("GET /api/hub-token", auth(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"hub": f.hub.URL, "token": "hubtok", "topics": []string{"dots/repo/org/app"},
			"expires_at": time.Now().Add(15 * time.Minute).UTC().Format(time.RFC3339)})
	}))
	f.Server = httptest.NewServer(mux)
	t.Cleanup(func() { f.Server.Close(); f.hub.Close() })
	return f
}

// fakeAWF runs jobs for a while: statuses are returned in order, then the last one repeats.
type fakeAWF struct {
	*httptest.Server
	mu       sync.Mutex
	runs     []map[string]any
	cancels  int
	statuses []string
	polls    int
	refuse   bool
}

func newFakeAWF(t *testing.T, statuses ...string) *fakeAWF {
	a := &fakeAWF{statuses: statuses}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/workflows/{scope}/{wf}/run", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.refuse {
			w.WriteHeader(422)
			return
		}
		var in struct{ Inputs map[string]any }
		json.NewDecoder(r.Body).Decode(&in)
		a.runs = append(a.runs, in.Inputs)
		w.WriteHeader(202)
		fmt.Fprintf(w, `{"body":{"execution_id":"x%d"}}`, len(a.runs))
	})
	mux.HandleFunc("GET /api/executions/{id}", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		st := a.statuses[min(a.polls, len(a.statuses)-1)]
		a.polls++
		fmt.Fprintf(w, `{"body":{"status":%q}}`, st)
	})
	mux.HandleFunc("DELETE /api/executions/{id}", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		a.cancels++
		a.mu.Unlock()
		w.WriteHeader(204)
	})
	a.Server = httptest.NewServer(mux)
	t.Cleanup(a.Server.Close)
	return a
}

func fast(w *worker) *worker {
	w.heartbeat, w.awfPoll, w.poll = 20*time.Millisecond, 10*time.Millisecond, 50*time.Millisecond
	return w
}

func find(t *testing.T, q *jobs.Queue, id string) jobs.Job {
	t.Helper()
	for _, j := range q.All() {
		if j.ID == id {
			return j
		}
	}
	t.Fatalf("no job %s", id)
	return jobs.Job{}
}

func TestWorkerClaimsRunsAndReportsDone(t *testing.T) {
	srv, awf := newFakeServer(t), newFakeAWF(t, "running", "completed")
	j, _ := srv.q.Enqueue("local", "review-pr", map[string]any{"repo": "org/app", "pr": "7"})
	w := fast(newWorker(srv.URL, tok, awf.URL))
	w.drain(context.Background())
	if got := find(t, srv.q, j.ID); got.State != jobs.Done || got.By == "" {
		t.Fatalf("job = %+v, want done by a worker", got)
	}
	if len(awf.runs) != 1 || awf.runs[0]["pr"] != "7" {
		t.Fatalf("awf was given %v", awf.runs)
	}
}

func TestWorkerReportsAFailedRunAndAnUnreachableAWF(t *testing.T) {
	srv, awf := newFakeServer(t), newFakeAWF(t, "failed")
	a, _ := srv.q.Enqueue("local", "review-pr", map[string]any{"repo": "org/app"})
	fast(newWorker(srv.URL, tok, awf.URL)).drain(context.Background())
	if got := find(t, srv.q, a.ID); got.State != jobs.Failed || !strings.Contains(got.Note, "failed") {
		t.Fatalf("job = %+v", got)
	}
	b, _ := srv.q.Enqueue("local", "review-pr", map[string]any{"repo": "org/app"})
	awf.refuse = true
	fast(newWorker(srv.URL, tok, awf.URL)).drain(context.Background())
	if got := find(t, srv.q, b.ID); got.State != jobs.Failed || !strings.Contains(got.Note, "refused") {
		t.Fatalf("job = %+v", got)
	}
}

func TestOnlyOneOfTwoWorkersRunsAJob(t *testing.T) {
	srv := newFakeServer(t)
	a1, a2 := newFakeAWF(t, "completed"), newFakeAWF(t, "completed")
	srv.q.Enqueue("local", "review-pr", map[string]any{"repo": "org/app"})
	var wg sync.WaitGroup
	for _, a := range []*fakeAWF{a1, a2} {
		wg.Add(1)
		go func() { defer wg.Done(); fast(newWorker(srv.URL, tok, a.URL)).drain(context.Background()) }()
	}
	wg.Wait()
	if n := len(a1.runs) + len(a2.runs); n != 1 {
		t.Fatalf("the job ran %d times, want once", n)
	}
}

func TestAWorkerStopsARunTheWatcherCancelled(t *testing.T) {
	srv, awf := newFakeServer(t), newFakeAWF(t, "running")
	j, _ := srv.q.Enqueue("local", "review-pr", map[string]any{"repo": "org/app"})
	go func() {
		time.Sleep(60 * time.Millisecond)
		srv.q.Cancel(j.ID)
	}()
	fast(newWorker(srv.URL, tok, awf.URL)).drain(context.Background())
	awf.mu.Lock()
	defer awf.mu.Unlock()
	if awf.cancels != 1 {
		t.Fatalf("the local run was cancelled %d times, want 1", awf.cancels)
	}
	if got := find(t, srv.q, j.ID); got.State != jobs.Cancelled {
		t.Fatalf("job = %+v", got)
	}
}

func TestAWorkerWithABadTokenDoesNothing(t *testing.T) {
	srv, awf := newFakeServer(t), newFakeAWF(t, "completed")
	srv.q.Enqueue("local", "review-pr", map[string]any{"repo": "org/app"})
	fast(newWorker(srv.URL, "dwt_wrong", awf.URL)).drain(context.Background())
	if len(awf.runs) != 0 {
		t.Fatal("a worker with a refused token ran a job")
	}
}

func TestHubMessageWakesTheWorkerBeforeItsPoll(t *testing.T) {
	srv, awf := newFakeServer(t), newFakeAWF(t, "completed")
	w := fast(newWorker(srv.URL, tok, awf.URL))
	w.poll = time.Hour // only the hub can wake it
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.run(ctx)
	time.Sleep(150 * time.Millisecond) // first drain: nothing pending
	j, _ := srv.q.Enqueue("local", "review-pr", map[string]any{"repo": "org/app"})
	srv.nudge <- struct{}{}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if find(t, srv.q, j.ID).State == jobs.Done {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the hub message did not wake the worker")
}
