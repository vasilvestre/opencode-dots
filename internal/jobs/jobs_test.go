package jobs

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

var mine = map[string]bool{"org/app": true}

func run(repo string) map[string]any { return map[string]any{"repo": repo, "pr": "7"} }

func TestFirstClaimWinsAndOthersAreRefused(t *testing.T) {
	q := Open("")
	j, err := q.Enqueue("local", "review-pr", run("Org/App"))
	if err != nil || j.Repo != "org/app" {
		t.Fatalf("enqueue = %+v, %v", j, err)
	}
	if got := q.Pending(mine); len(got) != 1 || got[0].ID != j.ID {
		t.Fatalf("pending = %+v", got)
	}
	if _, err := q.Claim(j.ID, "ada", mine); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Claim(j.ID, "bob", mine); !errors.Is(err, ErrTaken) {
		t.Fatalf("second claim = %v, want ErrTaken", err)
	}
	if len(q.Pending(mine)) != 0 {
		t.Fatal("a taken job must not be offered")
	}
}

func TestAWorkerOnlyClaimsItsRepositories(t *testing.T) {
	q := Open("")
	j, _ := q.Enqueue("local", "review-pr", run("other/repo"))
	if _, err := q.Claim(j.ID, "ada", mine); !errors.Is(err, ErrForbidden) {
		t.Fatalf("claim = %v, want ErrForbidden", err)
	}
	if len(q.Pending(mine)) != 0 {
		t.Fatal("another repository's job was offered")
	}
}

func TestALostWorkerGivesTheJobBack(t *testing.T) {
	q := Open("")
	now := time.Now()
	q.Now = func() time.Time { return now }
	j, _ := q.Enqueue("local", "review-pr", run("org/app"))
	q.Claim(j.ID, "ada", mine)
	now = now.Add(time.Minute)
	if _, err := q.Claim(j.ID, "ada", mine); err != nil { // the holder claiming again renews the lease
		t.Fatal(err)
	}
	now = now.Add(q.Lease + time.Second) // no more heartbeat
	if got := q.Pending(mine); len(got) != 1 {
		t.Fatalf("an abandoned job must come back: %+v", got)
	}
	if _, err := q.Claim(j.ID, "bob", mine); err != nil {
		t.Fatalf("takeover = %v", err)
	}
	if _, err := q.Claim(j.ID, "ada", mine); !errors.Is(err, ErrTaken) {
		t.Fatalf("the old holder renewing = %v, want ErrTaken", err)
	}
}

func TestCancelStopsPendingAndTellsTheWorker(t *testing.T) {
	q := Open("")
	a, _ := q.Enqueue("local", "review-pr", run("org/app"))
	if _, ok := q.Cancel(a.ID); !ok || len(q.Pending(mine)) != 0 {
		t.Fatal("a pending job must be cancellable")
	}
	b, _ := q.Enqueue("local", "review-pr", run("org/app"))
	q.Claim(b.ID, "ada", mine)
	q.Cancel(b.ID)
	if c, err := q.Claim(b.ID, "ada", mine); err != nil || c.State != Cancelled {
		t.Fatalf("renewing after a cancel = %+v, %v, want the cancelled job", c, err)
	}
	if _, ok := q.Cancel("job-unknown"); ok {
		t.Fatal("cancelling nothing")
	}
}

func TestFinishAndExpiry(t *testing.T) {
	q := Open("")
	now := time.Now()
	q.Now = func() time.Time { return now }
	j, _ := q.Enqueue("local", "review-pr", run("org/app"))
	q.Claim(j.ID, "ada", mine)
	if _, err := q.Finish(j.ID, "bob", Done, "", 0); !errors.Is(err, ErrNotYours) {
		t.Fatalf("finish by another = %v", err)
	}
	if _, err := q.Finish(j.ID, "ada", "weird", "", 0); err == nil {
		t.Fatal("a bad status must be refused")
	}
	if done, err := q.Finish(j.ID, "ada", Done, "ok", 0.42); err != nil || done.Cost != 0.42 {
		t.Fatalf("finish = %+v, %v: the cost must be kept", done, err)
	}
	stale, _ := q.Enqueue("local", "review-pr", run("org/app"))
	now = now.Add(q.PendingTTL + time.Hour)
	for _, x := range q.All() {
		if x.ID == stale.ID && x.State != Expired {
			t.Fatalf("an unclaimed job must expire, got %s", x.State)
		}
	}
	now = now.Add(q.Keep + time.Hour)
	if len(q.All()) != 0 {
		t.Fatal("finished jobs must be pruned")
	}
}

func TestEnqueueNeedsARepo(t *testing.T) {
	q := Open("")
	for _, in := range []map[string]any{{}, {"repo": "norepo"}} {
		if _, err := q.Enqueue("local", "review-pr", in); err == nil {
			t.Fatalf("%v accepted", in)
		}
	}
}

func TestQueueSurvivesARestartInAPrivateFile(t *testing.T) {
	dir := t.TempDir()
	q := Open(dir)
	j, _ := q.Enqueue("local", "review-pr", run("org/app"))
	if info, err := os.Stat(filepath.Join(dir, "jobs.json")); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("file = %v, %v", info, err)
	}
	if got := Open(dir).Pending(mine); len(got) != 1 || got[0].ID != j.ID {
		t.Fatalf("after restart = %+v", got)
	}
}

func TestAnEmergencyStopHoldsJobsBackAndCancelsAClaim(t *testing.T) {
	dir := t.TempDir()
	q := Open(dir)
	a, _ := q.Enqueue("local", "review-pr", run("org/app"))
	b, _ := q.Enqueue("local", "review-pr", run("other/repo"))
	os.MkdirAll(filepath.Join(dir, "pause"), 0o755)
	os.WriteFile(filepath.Join(dir, "pause", "org__app.json"), []byte("{}"), 0o644)
	all := map[string]bool{"org/app": true, "other/repo": true}
	if got := q.Pending(all); len(got) != 1 || got[0].ID != b.ID {
		t.Fatalf("a stopped repository must offer nothing, got %+v", got)
	}
	if _, err := q.Claim(a.ID, "ada", all); !errors.Is(err, ErrPaused) {
		t.Fatalf("claim on a stopped repository = %v, want ErrPaused", err)
	}
	os.Remove(filepath.Join(dir, "pause", "org__app.json"))
	if _, err := q.Claim(a.ID, "ada", all); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "pause", "all.json"), []byte("{}"), 0o644)
	if j, err := q.Claim(a.ID, "ada", all); err != nil || j.State != Cancelled {
		t.Fatalf("a held claim at a global stop = %+v, %v, want Cancelled", j, err)
	}
	if got := q.Pending(all); len(got) != 0 {
		t.Fatalf("a global stop offers nothing: %+v", got)
	}
}

// A worker that declares its agents is only offered the jobs it can run; one that declares none
// is not filtered. A job that names no provider runs on opencode, and "any" runs on whatever the
// worker has.
func TestJobsAreFilteredByTheWorkersAgents(t *testing.T) {
	q := Open("")
	with := func(provider string) Job {
		in := run("org/app")
		if provider != "" {
			in["provider"] = provider
		}
		j, err := q.Enqueue("local", "review-pr", in)
		if err != nil {
			t.Fatal(err)
		}
		return j
	}
	plain, claude, anyJob := with(""), with("claude"), with("any")
	ids := func(agents ...string) map[string]bool {
		out := map[string]bool{}
		for _, j := range q.Pending(mine, agents...) {
			out[j.ID] = true
		}
		return out
	}
	if got := ids("claude"); got[plain.ID] || !got[claude.ID] || !got[anyJob.ID] {
		t.Fatalf("claude worker sees %v", got)
	}
	if got := ids("opencode"); !got[plain.ID] || got[claude.ID] || !got[anyJob.ID] {
		t.Fatalf("opencode worker sees %v", got)
	}
	if got := ids(); len(got) != 3 {
		t.Fatalf("a worker that declares nothing sees %v", got)
	}
	if _, err := q.Claim(plain.ID, "bob", mine, "claude"); !errors.Is(err, ErrAgent) {
		t.Fatalf("claude worker claiming an opencode job = %v, want ErrAgent", err)
	}
	if _, err := q.Claim(claude.ID, "bob", mine, "claude"); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Claim(claude.ID, "bob", mine, "claude"); err != nil { // the heartbeat
		t.Fatal(err)
	}
}
