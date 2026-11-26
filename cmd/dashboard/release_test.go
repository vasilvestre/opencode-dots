package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const relID = "20261122T093000Z-comment-acme_app_12.txt"

// fakeAwf answers the workflow list and records each run request.
func fakeAwf(t *testing.T, status int) (*httptest.Server, *[]string) {
	var runs []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Write([]byte(`{"workflows":[{"name":"publish-held","scope":"local"}]}`))
			return
		}
		var body struct {
			Inputs map[string]string `json:"inputs"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		runs = append(runs, r.URL.Path+" id="+body.Inputs["id"])
		w.WriteHeader(status)
		w.Write([]byte(`{"body":{"execution_id":"exec-1234abcd"}}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &runs
}

func releaseReq(method, host, body string) *http.Request {
	r := httptest.NewRequest(method, "/api/autonomy/release", strings.NewReader(body))
	r.Host = host
	r.Header.Set("Content-Type", "application/json")
	return r
}

func TestAutonomyRelease(t *testing.T) {
	state := t.TempDir()
	os.MkdirAll(filepath.Join(state, "held", "released"), 0o755)
	os.WriteFile(filepath.Join(state, "held", relID), []byte("action: comment\nref: acme/app#12\n"), 0o644)
	done := "20261122T080000Z-comment-acme_app_3.txt"
	os.WriteFile(filepath.Join(state, "held", "released", done), []byte("action: comment\nref: acme/app#3\n"), 0o644)
	os.WriteFile(filepath.Join(state, "held", done), []byte("action: comment\n"), 0o644)
	srv, runs := fakeAwf(t, http.StatusAccepted)
	d := &dash{state: state, awf: srv.URL}

	nj := releaseReq("POST", "127.0.0.1:8099", `{"id":"`+relID+`"}`)
	nj.Header.Set("Content-Type", "text/plain")
	cases := []struct {
		name   string
		req    *http.Request
		status int
	}{
		{"GET", releaseReq("GET", "127.0.0.1:8099", ""), 405},
		{"foreign host", releaseReq("POST", "evil.example", `{"id":"`+relID+`"}`), 403},
		{"not json", nj, 415},
		{"bad id", releaseReq("POST", "127.0.0.1:8099", `{"id":"../x.txt"}`), 422},
		{"no such record", releaseReq("POST", "127.0.0.1:8099", `{"id":"20261122T093000Z-comment-none.txt"}`), 404},
		{"already released", releaseReq("POST", "127.0.0.1:8099", `{"id":"`+done+`"}`), 409},
	}
	for _, c := range cases {
		w := httptest.NewRecorder()
		d.autonomyRelease(w, c.req)
		if w.Code != c.status {
			t.Errorf("%s: %d, want %d (%s)", c.name, w.Code, c.status, w.Body)
		}
	}
	if len(*runs) != 0 {
		t.Fatalf("refused requests started runs: %v", *runs)
	}

	w := httptest.NewRecorder()
	d.autonomyRelease(w, releaseReq("POST", "127.0.0.1:8099", `{"id":"`+relID+`"}`))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "exec-1234abcd") {
		t.Fatalf("release: %d %s", w.Code, w.Body)
	}
	if len(*runs) != 1 || !strings.Contains((*runs)[0], "/publish-held/run id="+relID) {
		t.Fatalf("runs = %v", *runs)
	}
	// The dashboard never moves the record itself: the workflow does.
	if _, err := os.Stat(filepath.Join(state, "held", relID)); err != nil {
		t.Fatalf("record moved by the dashboard: %v", err)
	}
}

func TestAutonomyReleaseAwfRefuses(t *testing.T) {
	state := t.TempDir()
	os.MkdirAll(filepath.Join(state, "held"), 0o755)
	os.WriteFile(filepath.Join(state, "held", relID), []byte("action: comment\n"), 0o644)
	srv, _ := fakeAwf(t, http.StatusInternalServerError)
	d := &dash{state: state, awf: srv.URL}
	w := httptest.NewRecorder()
	d.autonomyRelease(w, releaseReq("POST", "127.0.0.1:8099", `{"id":"`+relID+`"}`))
	if w.Code != 502 {
		t.Fatalf("status %d, want 502", w.Code)
	}
}

func TestAutonomyListsReleased(t *testing.T) {
	state := t.TempDir()
	os.MkdirAll(filepath.Join(state, "held", "released"), 0o755)
	os.WriteFile(filepath.Join(state, "held", "released", relID), []byte("action: comment\nref: acme/app#12\n"), 0o644)
	d := &dash{state: state}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/autonomy", nil)
	r.Host = "127.0.0.1:8099"
	d.autonomy(w, r)
	var got map[string][]gateItem
	json.Unmarshal(w.Body.Bytes(), &got)
	if len(got["released"]) != 1 || got["released"][0].Ref != "acme/app#12" || len(got["held"]) != 0 {
		t.Fatalf("got %+v", got)
	}
}

func TestHealth(t *testing.T) {
	state := t.TempDir()
	d := &dash{state: state}
	get := func(host string) (int, map[string]any) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/api/health", nil)
		r.Host = host
		d.health(w, r)
		var m map[string]any
		json.Unmarshal(w.Body.Bytes(), &m)
		return w.Code, m
	}
	if code, m := get("127.0.0.1:8099"); code != 200 || m["present"] != false {
		t.Fatalf("absent: %d %v", code, m)
	}
	if code, _ := get("evil.example"); code != 403 {
		t.Fatalf("foreign host: %d", code)
	}
	p := filepath.Join(state, "health.json")
	os.WriteFile(p, []byte(`{"overall":"degraded","checks":[{"name":"awf","status":"ok","detail":"up"},{"name":"disk free","status":"warn","detail":"low"}]}`), 0o644)
	old := time.Now().Add(-45 * time.Minute)
	os.Chtimes(p, old, old)
	code, m := get("127.0.0.1:8099")
	if code != 200 || m["present"] != true || m["overall"] != "degraded" || len(m["checks"].([]any)) != 2 {
		t.Fatalf("present: %d %v", code, m)
	}
	if ts, _ := time.Parse(time.RFC3339, m["mtime"].(string)); time.Since(ts) < 44*time.Minute {
		t.Fatalf("mtime = %v", m["mtime"])
	}
	os.WriteFile(p, []byte("{not json"), 0o644)
	if _, m := get("127.0.0.1:8099"); m["present"] != false {
		t.Fatalf("garbage: %v", m)
	}
}
