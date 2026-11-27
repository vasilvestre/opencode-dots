package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// workerDo sends a request to the worker listener with a bearer token.
// instanceOf gives each test token its own instance id, as each worker process has one.
func instanceOf(secret string) string {
	if len(secret) < 6 {
		return "i-none"
	}
	return "i-" + strings.ToLower(secret[len(secret)-6:])
}

func workerDo(r *rig, method, path, secret, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("X-Worker-Instance", instanceOf(secret))
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}
	rec := httptest.NewRecorder()
	r.d.workerHandler().ServeHTTP(rec, req)
	return rec
}

func enqueueRun(t *testing.T, r *rig, repo string) string {
	t.Helper()
	body := `{"inputs":{"repo":"` + repo + `","pr":"7","sha":"abc"}}`
	rec := ask(r.h, "POST", "/queue/api/workflows/local/review-pr/run", "owner", "", body)
	if rec.Code != 202 {
		t.Fatalf("enqueue = %d %s", rec.Code, rec.Body)
	}
	var out struct {
		Body struct {
			ID string `json:"execution_id"`
		}
	}
	json.Unmarshal(rec.Body.Bytes(), &out)
	if !strings.HasPrefix(out.Body.ID, "job-") {
		t.Fatalf("execution id = %q", out.Body.ID)
	}
	return out.Body.ID
}

func pendingIDs(t *testing.T, r *rig, secret string) []string {
	t.Helper()
	rec := workerDo(r, "GET", "/api/jobs", secret, "")
	if rec.Code != 200 {
		t.Fatalf("pending = %d %s", rec.Code, rec.Body)
	}
	var out struct{ Jobs []struct{ ID string } }
	json.Unmarshal(rec.Body.Bytes(), &out)
	var ids []string
	for _, j := range out.Jobs {
		ids = append(ids, j.ID)
	}
	return ids
}

func TestQueueRoundTripBetweenTheWatcherAndAWorker(t *testing.T) {
	r := workerRig(t)
	ada, _ := issueToken(t, r, `{"label":"ada","repos":["`+openRepo+`"]}`)
	bob, _ := issueToken(t, r, `{"label":"bob","repos":["`+openRepo+`"]}`)
	id := enqueueRun(t, r, openRepo)

	if got := pendingIDs(t, r, ada); len(got) != 1 || got[0] != id {
		t.Fatalf("ada sees %v, want [%s]", got, id)
	}
	if rec := workerDo(r, "POST", "/api/jobs/"+id+"/claim", ada, ""); rec.Code != 200 {
		t.Fatalf("ada claim = %d %s", rec.Code, rec.Body)
	}
	if rec := workerDo(r, "POST", "/api/jobs/"+id+"/claim", bob, ""); rec.Code != 409 {
		t.Fatalf("bob claim = %d, want 409", rec.Code)
	}
	if got := pendingIDs(t, r, bob); len(got) != 0 {
		t.Fatalf("a taken job is still offered: %v", got)
	}
	if rec := workerDo(r, "POST", "/api/jobs/"+id+"/heartbeat", ada, ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"cancelled":false`) {
		t.Fatalf("heartbeat = %d %s", rec.Code, rec.Body)
	}
	if rec := workerDo(r, "POST", "/api/jobs/"+id+"/finish", bob, `{"status":"done"}`); rec.Code != 409 {
		t.Fatalf("bob finishing ada's job = %d, want 409", rec.Code)
	}
	if rec := workerDo(r, "POST", "/api/jobs/"+id+"/finish", ada, `{"status":"done","note":"ok"}`); rec.Code != 200 {
		t.Fatalf("finish = %d %s", rec.Code, rec.Body)
	}
	list := r.owner("/api/queue")
	if list.Code != 200 || !strings.Contains(list.Body.String(), `"state":"done"`) {
		t.Fatalf("owner list = %d %s", list.Code, list.Body)
	}
}

func TestQueueKeepsRepositoriesApart(t *testing.T) {
	r := workerRig(t)
	ada, _ := issueToken(t, r, `{"label":"ada","repos":["`+openRepo+`"]}`)
	other := enqueueRun(t, r, hiddenRepo)
	if got := pendingIDs(t, r, ada); len(got) != 0 {
		t.Fatalf("a repository outside the token was offered: %v", got)
	}
	if rec := workerDo(r, "POST", "/api/jobs/"+other+"/claim", ada, ""); rec.Code != 403 {
		t.Fatalf("claim outside the token = %d, want 403", rec.Code)
	}
}

func TestQueueNeedsAWorkerTokenAndAnOwner(t *testing.T) {
	r := workerRig(t)
	r.signIn()
	id := enqueueRun(t, r, openRepo)
	for _, tok := range []string{"", workerPrefix + "nope"} {
		if rec := workerDo(r, "GET", "/api/jobs", tok, ""); rec.Code != 401 {
			t.Errorf("jobs with token %q = %d, want 401", tok, rec.Code)
		}
		if rec := workerDo(r, "POST", "/api/jobs/"+id+"/claim", tok, ""); rec.Code != 401 {
			t.Errorf("claim with token %q = %d, want 401", tok, rec.Code)
		}
	}
	// A visitor cannot enqueue or read the queue: it is the watcher's and the owner's.
	if rec := ask(r.h, "POST", "/queue/api/workflows/local/review-pr/run", "visitor", r.cookie, `{"inputs":{"repo":"`+openRepo+`"}}`); rec.Code != 403 {
		t.Errorf("a visitor enqueueing = %d, want 403", rec.Code)
	}
	if rec := ask(r.h, "GET", "/api/queue", "visitor", r.cookie, ""); rec.Code != 403 {
		t.Errorf("a visitor reading the queue = %d, want 403", rec.Code)
	}
	if rec := ask(r.h, "POST", "/queue/api/workflows/local/review-pr/run", "owner", "", `{"inputs":{}}`); rec.Code != 422 {
		t.Errorf("a run without a repo = %d, want 422", rec.Code)
	}
}

func TestQueueCancelStopsAPendingJobAndSignalsATaken(t *testing.T) {
	r := workerRig(t)
	ada, _ := issueToken(t, r, `{"label":"ada"}`)
	a := enqueueRun(t, r, openRepo)
	if rec := ask(r.h, "DELETE", "/queue/api/executions/"+a, "owner", "", ""); rec.Code != 204 {
		t.Fatalf("cancel = %d", rec.Code)
	}
	if got := pendingIDs(t, r, ada); len(got) != 0 {
		t.Fatalf("a cancelled job is still offered: %v", got)
	}
	b := enqueueRun(t, r, openRepo)
	workerDo(r, "POST", "/api/jobs/"+b+"/claim", ada, "")
	ask(r.h, "DELETE", "/queue/api/executions/"+b, "owner", "", "")
	if rec := workerDo(r, "POST", "/api/jobs/"+b+"/heartbeat", ada, ""); !strings.Contains(rec.Body.String(), `"cancelled":true`) {
		t.Fatalf("the worker must be told: %d %s", rec.Code, rec.Body)
	}
	if rec := ask(r.h, "DELETE", "/queue/api/executions/job-unknown", "owner", "", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("cancelling an unknown id = %d, want 204 like awf", rec.Code)
	}
}

func TestQueueShowsRunsLikeAnAwfExecutor(t *testing.T) {
	r := workerRig(t)
	ada, _ := issueToken(t, r, `{"label":"ada"}`)
	running := enqueueRun(t, r, openRepo)
	workerDo(r, "POST", "/api/jobs/"+running+"/claim", ada, "")
	ended := enqueueRun(t, r, openRepo)
	workerDo(r, "POST", "/api/jobs/"+ended+"/claim", ada, "")
	workerDo(r, "POST", "/api/jobs/"+ended+"/finish", ada, `{"status":"done"}`)

	var ex struct {
		Body struct{ Executions []map[string]any }
	}
	json.Unmarshal(r.owner("/queue/api/executions").Body.Bytes(), &ex)
	if len(ex.Body.Executions) != 1 || ex.Body.Executions[0]["execution_id"] != running || ex.Body.Executions[0]["status"] != "running" {
		t.Fatalf("executions = %v", ex.Body.Executions)
	}
	var h struct {
		Body struct{ Entries []map[string]any }
	}
	json.Unmarshal(r.owner("/queue/api/history").Body.Bytes(), &h)
	if len(h.Body.Entries) != 1 || h.Body.Entries[0]["status"] != "completed" || h.Body.Entries[0]["target"] != openRepo+"#7" {
		t.Fatalf("history = %v", h.Body.Entries)
	}
	if got := ask(r.h, "GET", "/queue/api/history", "visitor", "", "").Code; got != 401 && got != 403 {
		t.Fatalf("a visitor reading the history = %d", got)
	}
}

// Cost vanished from the Cost tab and the Home page twice: runs that a worker did on another
// machine have no log on the server, so only the price the worker reports can show them. These
// tests keep that path, for the owner and for a visitor, from breaking again.
func TestWorkerRunsCountInUsageAndHomeAndStayScoped(t *testing.T) {
	r := workerRig(t)
	type usage struct {
		Total    float64
		Runs     int
		Unpriced int
		Groups   []usageOne
	}
	read := func(who string) usage {
		var u usage
		rec := r.owner("/api/usage")
		if who == "visitor" {
			rec = r.visitor("/api/usage")
		}
		json.Unmarshal(rec.Body.Bytes(), &u)
		return u
	}
	group := func(u usage, key string) *usageOne {
		for i := range u.Groups {
			if u.Groups[i].Key == key {
				return &u.Groups[i]
			}
		}
		return nil
	}
	r.signIn()
	baseOwner, baseVisitor := read("owner"), read("visitor")

	ada, _ := issueToken(t, r, `{"label":"ada"}`)
	for repo, cost := range map[string]float64{openRepo: 0.5, hiddenRepo: 9} {
		id := enqueueRun(t, r, repo)
		workerDo(r, "POST", "/api/jobs/"+id+"/claim", ada, "")
		workerDo(r, "POST", "/api/jobs/"+id+"/finish", ada, fmt.Sprintf(`{"status":"done","cost_usd":%v}`, cost))
	}
	owner := read("owner")
	if owner.Total-baseOwner.Total != 9.5 || owner.Runs-baseOwner.Runs != 2 {
		t.Fatalf("owner usage went from %+v to %+v, want +9.5 over 2 runs", baseOwner.Total, owner.Total)
	}
	open, hidden := group(owner, openRepo+"#7"), group(owner, hiddenRepo+"#7")
	if open == nil || hidden == nil || open.Cost != 0.5 || hidden.Cost != 9 || open.Unpriced != 0 || open.Workflows[0].Workflow != "review-pr" {
		t.Fatalf("groups = %+v / %+v", open, hidden)
	}
	visitor := read("visitor")
	if visitor.Total-baseVisitor.Total != 0.5 || group(visitor, hiddenRepo+"#7") != nil || group(visitor, openRepo+"#7") == nil {
		t.Fatalf("visitor usage = %+v, want the visible repository only", visitor)
	}
	var home []struct {
		Repo   string
		Cost7d float64
	}
	json.Unmarshal(r.owner("/api/home").Body.Bytes(), &home)
	seen := map[string]float64{}
	for _, h := range home {
		seen[h.Repo] = h.Cost7d
	}
	if seen[openRepo] < 0.5 || seen[hiddenRepo] < 9 {
		t.Fatalf("home cost7d = %v, the workers' runs are missing", seen)
	}
	// An unreported price is counted as unpriced, never as free.
	id := enqueueRun(t, r, openRepo)
	workerDo(r, "POST", "/api/jobs/"+id+"/claim", ada, "")
	workerDo(r, "POST", "/api/jobs/"+id+"/finish", ada, `{"status":"done"}`)
	if got := read("owner").Unpriced - owner.Unpriced; got != 1 {
		t.Fatalf("unpriced grew by %d, want 1", got)
	}
}
