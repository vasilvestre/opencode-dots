package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"opencode-dots/internal/store"
)

func auditRequest(method, path, host, login, body string) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Host = host
	if login != "" {
		r.Header.Set(tailscaleLoginHeader, login)
	}
	return r
}

func auditRows(t *testing.T, state string) []store.AuditEntry {
	t.Helper()
	st, err := store.Open(filepath.Join(state, "dots.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	rows, err := st.RecentAudit(context.Background(), 500)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestAWriteIsAuditedWithItsTargetAndOutcome(t *testing.T) {
	d := &dash{state: t.TempDir(), allow: map[string]bool{}}
	h := d.audited("/api/save", "", func(w http.ResponseWriter, r *http.Request) {
		auditTarget(r, "workflows/a.yaml")
		http.Error(w, "refused path", http.StatusForbidden)
	})
	h(httptest.NewRecorder(), auditRequest(http.MethodPut, "/api/save", "127.0.0.1:8099", "", "{}"))

	rows := auditRows(t, d.state)
	if len(rows) != 1 {
		t.Fatalf("rows = %+v", rows)
	}
	got := rows[0]
	if got.Endpoint != "PUT /api/save" || got.Target != "workflows/a.yaml" || got.Caller != "local" || got.Outcome != 403 {
		t.Errorf("entry = %+v", got)
	}
	if time.Since(got.At) > time.Minute {
		t.Errorf("the time is not the server's: %v", got.At)
	}
}

func TestAnHandlerThatWritesNothingIsA200AndAReadIsNotRecorded(t *testing.T) {
	d := &dash{state: t.TempDir(), allow: map[string]bool{}}
	h := d.audited("/api/models", "model-presets", func(w http.ResponseWriter, r *http.Request) {})
	h(httptest.NewRecorder(), auditRequest(http.MethodGet, "/api/models", "127.0.0.1:8099", "", ""))
	if _, err := os.Stat(filepath.Join(d.state, "dots.db")); err == nil {
		t.Fatal("a read opened the audit log")
	}
	h(httptest.NewRecorder(), auditRequest(http.MethodPut, "/api/models", "127.0.0.1:8099", "", ""))
	rows := auditRows(t, d.state)
	if len(rows) != 1 || rows[0].Outcome != 200 || rows[0].Target != "model-presets" {
		t.Errorf("rows = %+v", rows)
	}
}

func TestTheCallerIsTheTailnetLoginOnlyOnATrustedRequest(t *testing.T) {
	d := &dash{state: t.TempDir(), allow: map[string]bool{"box.example.ts.net": true}}
	h := d.audited("/api/save", "", func(w http.ResponseWriter, r *http.Request) {})
	for _, c := range []struct{ host, login, origin string }{
		{"box.example.ts.net", "ada@example.com", ""},                    // through tailscale serve
		{"127.0.0.1:8099", "", ""},                                       // at the machine
		{"evil.example", "root@example.com", ""},                         // a host nobody allowed
		{"127.0.0.1:8099", "mallory@example.com", "http://evil.example"}, // a foreign origin
	} {
		r := auditRequest(http.MethodPut, "/api/save", c.host, c.login, "")
		if c.origin != "" {
			r.Header.Set("Origin", c.origin)
		}
		h(httptest.NewRecorder(), r)
	}
	var callers []string
	for _, row := range auditRows(t, d.state) {
		callers = append(callers, row.Caller)
	}
	// Newest first.
	want := []string{"local", "local", "local", "ada@example.com"}
	if strings.Join(callers, ",") != strings.Join(want, ",") {
		t.Errorf("callers = %v, want %v", callers, want)
	}
}

func TestAuditNeverHoldsABodyOrASecret(t *testing.T) {
	d := &dash{state: t.TempDir(), allow: map[string]bool{}}
	h := d.audited("/api/notifications", "notifications", func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Write([]byte("saved"))
	})
	r := auditRequest(http.MethodPut, "/api/notifications?token=sekret-query", "127.0.0.1:8099", "", `{"webhook":"https://hooks.example/sekret-body"}`)
	r.Header.Set("Authorization", "Bearer sekret-header")
	h(httptest.NewRecorder(), r)
	rows := auditRows(t, d.state)
	if len(rows) != 1 {
		t.Fatalf("rows = %+v", rows)
	}
	all := rows[0].Endpoint + rows[0].Target + rows[0].Caller
	if strings.Contains(all, "sekret") || strings.Contains(all, "hooks.example") {
		t.Errorf("the entry holds a secret: %+v", rows[0])
	}
}

func TestAStoreThatCannotTakeTheRowNeverBlocksTheEndpoint(t *testing.T) {
	state := t.TempDir()
	if err := os.Mkdir(filepath.Join(state, "dots.db"), 0o700); err != nil { // not a database
		t.Fatal(err)
	}
	d := &dash{state: state, allow: map[string]bool{}}
	h := d.audited("/api/save", "", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte("done"))
	})
	rec := httptest.NewRecorder()
	h(rec, auditRequest(http.MethodPut, "/api/save", "127.0.0.1:8099", "", ""))
	if rec.Code != http.StatusCreated || rec.Body.String() != "done" {
		t.Errorf("the endpoint answered %d %q", rec.Code, rec.Body.String())
	}
}

func TestEveryWriteHandlerIsWiredThroughTheAudit(t *testing.T) {
	// A refused write is recorded too: the real handlers, refused for a foreign host.
	d := &dash{state: t.TempDir(), root: t.TempDir(), allow: map[string]bool{}}
	for _, c := range []struct {
		method, path string
		h            http.HandlerFunc
	}{
		{http.MethodPut, "/api/save", d.saveFile},
		{http.MethodPut, "/api/models", d.models},
		{http.MethodPut, "/api/notifications", d.notifications},
		{http.MethodPost, "/api/autonomy/release", d.autonomyRelease},
		{http.MethodPost, "/api/ask", d.ask},
		{http.MethodPost, "/api/ask/answer", d.askAnswer},
		{http.MethodPatch, "/api/repos/config", d.reposConfig},
	} {
		rec := httptest.NewRecorder()
		d.audited(c.path, "", c.h)(rec, auditRequest(c.method, c.path, "evil.example", "mallory@example.com", `{}`))
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s answered %d, want 403", c.method, c.path, rec.Code)
		}
	}
	rows := auditRows(t, d.state)
	if len(rows) != 7 {
		t.Fatalf("rows = %d, want 7", len(rows))
	}
	for _, row := range rows {
		if row.Caller != "local" || row.Outcome != 403 {
			t.Errorf("entry = %+v", row)
		}
	}
}

func TestAuditListsNewestFirstWithALimit(t *testing.T) {
	d := &dash{state: t.TempDir(), allow: map[string]bool{}}
	st, err := store.Open(filepath.Join(d.state, "dots.db"))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 600; i++ {
		st.RecordAudit(context.Background(), "PUT /api/save", "f"+string(rune('a'+i%26)), "local", 200)
	}
	st.Close()
	get := func(query, host string) (*httptest.ResponseRecorder, []auditView) {
		rec := httptest.NewRecorder()
		d.audit(rec, auditRequest(http.MethodGet, "/api/audit"+query, host, "", ""))
		var out []auditView
		json.Unmarshal(rec.Body.Bytes(), &out)
		return rec, out
	}
	if _, out := get("", "127.0.0.1:8099"); len(out) != 100 || out[0].ID < out[1].ID {
		t.Errorf("default = %d entries, first ids %d %d", len(out), out[0].ID, out[1].ID)
	}
	if _, out := get("?limit=5", "localhost:8099"); len(out) != 5 {
		t.Errorf("limit 5 = %d", len(out))
	}
	if _, out := get("?limit=9999", "127.0.0.1:8099"); len(out) != 500 {
		t.Errorf("limit 9999 = %d, want the cap 500", len(out))
	}
	if _, out := get("?limit=abc", "127.0.0.1:8099"); len(out) != 100 {
		t.Errorf("bad limit = %d, want the default", len(out))
	}
	if rec, _ := get("", "evil.example"); rec.Code != http.StatusForbidden {
		t.Errorf("a foreign host got %d", rec.Code)
	}
	rec := httptest.NewRecorder()
	d.audit(rec, auditRequest(http.MethodPost, "/api/audit", "127.0.0.1:8099", "", ""))
	if rec.Code != http.StatusForbidden {
		t.Errorf("POST got %d", rec.Code)
	}
}

func TestAuditWithoutADatabaseIsAnEmptyList(t *testing.T) {
	rec := httptest.NewRecorder()
	(&dash{state: t.TempDir()}).audit(rec, auditRequest(http.MethodGet, "/api/audit", "127.0.0.1:8099", "", ""))
	if body := strings.TrimSpace(rec.Body.String()); body != "[]" {
		t.Fatalf("body = %q", body)
	}
}
