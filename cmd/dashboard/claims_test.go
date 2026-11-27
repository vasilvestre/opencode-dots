package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"opencode-dots/internal/store"
)

// A claim the dashboard shows is one a shell step refuses to push to, so the list has
// to carry the repository and the branch, not only the work item.
func TestClaimsListTheLiveOnes(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "dots.db"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	human, _ := st.EnsureActor(ctx, store.Actor{Kind: "human", DisplayName: "ada"})
	p, _, _ := st.UpsertProject(ctx, "default", "acme/app", 0, nil)
	w, _ := st.CreateWorkItem(ctx, p.ID, "Fix login", human.ID, "github:acme/app#7")
	if _, err := st.TakeClaim(ctx, w.ID, human.ID, "laptop", "fix-login", 0); err != nil {
		t.Fatal(err)
	}
	st.Close()

	rec := httptest.NewRecorder()
	(&dash{state: dir}).claims(rec, httptest.NewRequest(http.MethodGet, "/api/claims", nil))

	var got []claimView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("%v: %s", err, rec.Body.String())
	}
	if len(got) != 1 || got[0].Repo != "acme/app" || got[0].Branch != "fix-login" || got[0].Machine != "laptop" {
		t.Fatalf("claims = %+v", got)
	}
}

func TestClaimsWithoutADatabaseIsAnEmptyList(t *testing.T) {
	rec := httptest.NewRecorder()
	(&dash{state: t.TempDir()}).claims(rec, httptest.NewRequest(http.MethodGet, "/api/claims", nil))
	if body := rec.Body.String(); body != "[]\n" && body != "[]" {
		t.Fatalf("body = %q", body)
	}
}
