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
