package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestRemoteRunsKeepLastSeenWhenExecutorIsDown(t *testing.T) {
	var up = true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !up {
			http.Error(w, "down", http.StatusBadGateway)
			return
		}
		switch r.URL.Path {
		case "/api/executions":
			w.Write([]byte(`{"body":{"executions":[{"execution_id":"e1","workflow_name":"fix-issue","status":"running","started_at":"2026-11-26T18:47:52.4+01:00"}]}}`))
		case "/api/history":
			w.Write([]byte(`{"body":{"entries":[{"id":"h1","workflow_name":"fix-issue","status":"completed","started_at":"2026-11-26T18:47:52.9+01:00"}]}}`))
		}
	}))
	defer srv.Close()

	root, state := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(root, "repos.yaml"), []byte("version: 1\nsettings:\n  executors:\n    workstation: "+srv.URL+"\nrepos:\n  a/b:\n    executor: workstation\n    fix_issue: true\n"), 0o644)
	d := &dash{root: root, state: state}

	d.pollExecutors()
	snap := d.remote["workstation"]
	if !snap.Up || len(snap.Entries) != 1 || snap.Entries[0]["execution_id"] != "e1" {
		t.Fatalf("snapshot = %+v", snap)
	}

	up = false
	d.remoteAt = d.remoteAt.AddDate(0, 0, -1)
	d.pollExecutors()
	snap = d.remote["workstation"]
	if snap.Up || len(snap.Entries) != 1 {
		t.Fatalf("down executor lost its last runs: %+v", snap)
	}

	// A restart reads the file back.
	d2 := &dash{root: root, state: state}
	if got := d2.loadSnapshot("workstation"); len(got.Entries) != 1 {
		t.Fatalf("snapshot not persisted: %+v", got)
	}
	if got := tagged(snap.Executions[0], "workstation", false); got["status"] != "unknown" {
		t.Fatalf("stale running run = %v", got["status"])
	}
}

func TestViaExecutorServesTheExecutorsLog(t *testing.T) {
	exec := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("log of " + r.URL.Query().Get("id")))
	}))
	defer exec.Close()
	id := "aaaabbbb-1234-1234-1234-123456789012"
	d := &dash{state: t.TempDir(), execDash: parseExecutorDashboards("workstation=" + exec.URL + ",bad=nope"),
		remote: map[string]remoteSnapshot{"workstation": {Executions: []map[string]any{{"execution_id": id}}}}}
	local := func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("local")) }
	get := func() string {
		rec := httptest.NewRecorder()
		d.viaExecutor(local)(rec, httptest.NewRequest(http.MethodGet, "/api/log?id="+id, nil))
		return rec.Body.String()
	}
	if got := get(); got != "log of "+id {
		t.Fatalf("remote run served %q", got)
	}
	os.MkdirAll(filepath.Join(d.state, "logs"), 0o755)
	os.WriteFile(filepath.Join(d.state, "logs", id+".jsonl"), []byte("x"), 0o644)
	if got := get(); got != "local" {
		t.Fatalf("a run with a local log must stay local, got %q", got)
	}
	if len(d.execDash) != 1 {
		t.Fatalf("invalid address kept: %v", d.execDash)
	}
}
