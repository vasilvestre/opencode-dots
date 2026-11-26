package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"opencode-dots/internal/store"
)

func TestWorkersListLiveBeforeExpired(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "dots.db"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := st.RegisterWorker(ctx, "a-old", "desk", nil, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ExpireWorker(ctx, "a-old"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RegisterWorker(ctx, "b-live", "laptop", []byte(`{"models":["local"]}`), time.Hour); err != nil {
		t.Fatal(err)
	}
	st.Close()

	rec := httptest.NewRecorder()
	(&dash{state: dir}).workers(rec, auditRequest(http.MethodGet, "/api/workers", "127.0.0.1:8099", "", ""))
	var got []workerView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("%v: %s", err, rec.Body.String())
	}
	if len(got) != 2 || got[0].Name != "b-live" || !got[0].Live || got[1].Name != "a-old" || got[1].Live {
		t.Fatalf("workers = %+v", got)
	}
	if string(got[0].Capabilities) != `{"models":["local"]}` || got[0].Machine != "laptop" || got[0].ExpiresAt == "" {
		t.Errorf("worker = %+v", got[0])
	}
}

func TestWorkersAreReadOnlyAndTrustTheHost(t *testing.T) {
	d := &dash{state: t.TempDir()}
	rec := httptest.NewRecorder()
	d.workers(rec, auditRequest(http.MethodGet, "/api/workers", "evil.example", "", ""))
	if rec.Code != http.StatusForbidden {
		t.Errorf("a foreign host got %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	d.workers(rec, auditRequest(http.MethodPost, "/api/workers", "127.0.0.1:8099", "", ""))
	if rec.Code != http.StatusForbidden {
		t.Errorf("POST got %d", rec.Code)
	}
}

func TestWorkersWithoutADatabaseIsAnEmptyList(t *testing.T) {
	rec := httptest.NewRecorder()
	(&dash{state: t.TempDir()}).workers(rec, auditRequest(http.MethodGet, "/api/workers", "127.0.0.1:8099", "", ""))
	if body := strings.TrimSpace(rec.Body.String()); body != "[]" {
		t.Fatalf("body = %q", body)
	}
}
