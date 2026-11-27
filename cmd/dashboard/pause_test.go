package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The emergency stop is the one control that must work when everything else misbehaves. These tests
// hold the three places it acts in the dashboard: the queue offers nothing, a claim in progress is
// cancelled, and only the owner can flip it.
func TestPauseStopsTheQueueForARepositoryAndForEverything(t *testing.T) {
	r := workerRig(t)
	ada, _ := issueToken(t, r, `{"label":"ada"}`)
	id := enqueueRun(t, r, openRepo)
	other := enqueueRun(t, r, hiddenRepo)

	stop := ask(r.h, "POST", "/api/pause", "owner", "", `{"action":"stop","repo":"`+openRepo+`","reason":"bad merge"}`)
	if stop.Code != 200 || !r.d.isPaused(openRepo) || r.d.isPaused(hiddenRepo) {
		t.Fatalf("stop = %d %s", stop.Code, stop.Body)
	}
	if got := pendingIDs(t, r, ada); len(got) != 1 || got[0] != other {
		t.Fatalf("a stopped repository must offer no job: %v", got)
	}
	if rec := workerDo(r, "POST", "/api/jobs/"+id+"/claim", ada, ""); rec.Code != 423 {
		t.Fatalf("claiming a stopped repository's job = %d, want 423", rec.Code)
	}

	ask(r.h, "POST", "/api/pause", "owner", "", `{"action":"stop","reason":"everything"}`)
	if got := pendingIDs(t, r, ada); len(got) != 0 {
		t.Fatalf("a global stop must offer nothing: %v", got)
	}
	var st struct {
		All   *pauseRecord
		Repos map[string]*pauseRecord
	}
	json.Unmarshal(r.owner("/api/pause").Body.Bytes(), &st)
	if st.All == nil || st.All.Reason != "everything" || st.Repos[openRepo] == nil || st.Repos[openRepo].Reason != "bad merge" {
		t.Fatalf("status = %+v", st)
	}

	ask(r.h, "POST", "/api/pause", "owner", "", `{"action":"resume"}`)
	ask(r.h, "POST", "/api/pause", "owner", "", `{"action":"resume","repo":"`+openRepo+`"}`)
	if got := pendingIDs(t, r, ada); len(got) != 2 {
		t.Fatalf("after resuming both jobs are offered again: %v", got)
	}
}

func TestPauseCancelsARunInProgressAtItsNextHeartbeat(t *testing.T) {
	r := workerRig(t)
	ada, _ := issueToken(t, r, `{"label":"ada"}`)
	id := enqueueRun(t, r, openRepo)
	workerDo(r, "POST", "/api/jobs/"+id+"/claim", ada, "")
	ask(r.h, "POST", "/api/pause", "owner", "", `{"action":"stop","repo":"`+openRepo+`"}`)
	rec := workerDo(r, "POST", "/api/jobs/"+id+"/heartbeat", ada, "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"cancelled":true`) {
		t.Fatalf("the worker must be told to stop: %d %s", rec.Code, rec.Body)
	}
}

func TestOnlyTheOwnerCanStopOrResume(t *testing.T) {
	r := workerRig(t)
	r.signIn()
	for _, body := range []string{`{"action":"stop"}`, `{"action":"resume"}`} {
		if rec := ask(r.h, "POST", "/api/pause", "visitor", r.cookie, body); rec.Code != 403 {
			t.Errorf("a visitor sending %s = %d, want 403", body, rec.Code)
		}
	}
	if rec := ask(r.h, "GET", "/api/pause", "visitor", r.cookie, ""); rec.Code != 403 {
		t.Errorf("a visitor reading it = %d, want 403", rec.Code)
	}
	if r.d.isPaused(openRepo) {
		t.Fatal("a visitor stopped the dot")
	}
	for name, body := range map[string]string{"bad action": `{"action":"x"}`, "bad repo": `{"action":"stop","repo":"../x"}`, "malformed": `{`} {
		if rec := ask(r.h, "POST", "/api/pause", "owner", "", body); rec.Code != 400 {
			t.Errorf("%s = %d, want 400", name, rec.Code)
		}
	}
}

// A stop written by bin/pause (a file) and one written by the dashboard are the same thing.
func TestAStopFileWrittenByTheScriptCountsToo(t *testing.T) {
	r := workerRig(t)
	dir := filepath.Join(r.d.state, "pause")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, pauseSlug(openRepo)+".json"), []byte("not json"), 0o644)
	if !r.d.isPaused(openRepo) || r.d.isPaused(hiddenRepo) {
		t.Fatal("an unreadable stop file must still stop its repository, and only it")
	}
}
