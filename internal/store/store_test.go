package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "sub", "dots.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func actor(t *testing.T, s *Store) Actor {
	t.Helper()
	a, err := s.EnsureActor(context.Background(), Actor{Kind: "human", DisplayName: "Ada", Identities: map[string]string{"github": "ada"}})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestOpenCreatesAPrivateFileAndMigrates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a", "b", "dots.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := s.schemaVersion(context.Background()); v != len(migrations) {
		t.Errorf("schema version = %d, want %d", v, len(migrations))
	}
	s.Close()
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("database file mode = %v, %v, want 0600", info.Mode().Perm(), err)
	}
	// Opening again must not migrate twice or lose data.
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if v, _ := s.schemaVersion(context.Background()); v != len(migrations) {
		t.Errorf("after reopening, schema version = %d", v)
	}
}

func TestADatabaseFromANewerBuildIsRefused(t *testing.T) {
	s := open(t)
	if _, err := s.db.Exec(`INSERT INTO schema_version (version) VALUES (?)`, len(migrations)+1); err != nil {
		t.Fatal(err)
	}
	if err := s.migrate(context.Background()); err == nil {
		t.Fatal("a database newer than the build must not be migrated")
	}
}

func TestProjectsKeepFileOrderAndReportChanges(t *testing.T) {
	s, ctx := open(t), context.Background()
	for i, repo := range []string{"acme/zeta", "acme/alpha", "acme/mid"} {
		if _, changed, err := s.UpsertProject(ctx, "default", repo, i, map[string]any{"tend": map[string]any{}}); err != nil || !changed {
			t.Fatalf("insert %s: changed=%v err=%v", repo, changed, err)
		}
	}
	got, err := s.Projects(ctx, "default")
	if err != nil {
		t.Fatal(err)
	}
	order := []string{got[0].Repo, got[1].Repo, got[2].Repo}
	if order[0] != "acme/zeta" || order[1] != "acme/alpha" || order[2] != "acme/mid" {
		t.Errorf("order = %v, want the file order, not alphabetical", order)
	}
	if _, changed, _ := s.UpsertProject(ctx, "default", "acme/alpha", 1, map[string]any{"tend": map[string]any{}}); changed {
		t.Error("the same project again must report no change")
	}
	p, changed, err := s.UpsertProject(ctx, "default", "acme/alpha", 1, map[string]any{"review": map[string]any{}})
	if err != nil || !changed || p.Features["review"] == nil || p.Features["tend"] != nil {
		t.Errorf("changed features: %+v changed=%v err=%v", p.Features, changed, err)
	}
	if _, err := s.Projects(ctx, "other"); err != nil {
		t.Errorf("an empty workspace is not an error: %v", err)
	}
}

func TestProjectNamesAreChecked(t *testing.T) {
	s := open(t)
	for _, bad := range []string{"", "noslash", "acme/has space"} {
		if _, _, err := s.UpsertProject(context.Background(), "default", bad, 0, nil); err == nil {
			t.Errorf("%q accepted as a repository", bad)
		}
	}
}

func TestWorkspaceSettingsRoundTrip(t *testing.T) {
	s, ctx := open(t), context.Background()
	if got, err := s.WorkspaceSettings(ctx, "default"); err != nil || string(got) != "{}" {
		t.Errorf("empty settings = %s, %v", got, err)
	}
	if err := s.SetWorkspaceSettings(ctx, "default", []byte(`{"max_per_run":6}`)); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.WorkspaceSettings(ctx, "default"); string(got) != `{"max_per_run":6}` {
		t.Errorf("settings = %s", got)
	}
}

func TestOneReferenceNamesOneWorkItem(t *testing.T) {
	s, ctx := open(t), context.Background()
	a := actor(t, s)
	p, _, _ := s.UpsertProject(ctx, "default", "acme/app", 0, nil)
	first, err := s.CreateWorkItem(ctx, p.ID, "Fix the login", a.ID, "jira:ABC-12", "branch:acme/app:fix-login")
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Refs) != 2 || first.State != "open" || first.CreatedBy != a.ID {
		t.Errorf("work item = %+v", first)
	}
	// A second work item cannot take a reference the first one holds, and is not created.
	_, err = s.CreateWorkItem(ctx, p.ID, "Duplicate", a.ID, "github:acme/app#7", "jira:ABC-12")
	if !errors.Is(err, ErrRefTaken) {
		t.Fatalf("error = %v, want ErrRefTaken", err)
	}
	if _, err := s.WorkItemByRef(ctx, "github:acme/app#7"); !errors.Is(err, ErrNotFound) {
		t.Errorf("the failed creation left a reference behind: %v", err)
	}
	// Attaching works for a free reference and fails for a held one.
	if _, err := s.AttachRef(ctx, first.ID, "github:acme/app#7", "pull_request.opened", a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AttachRef(ctx, first.ID, "github:acme/app#7", "", a.ID); !errors.Is(err, ErrRefTaken) {
		t.Errorf("attaching twice: %v", err)
	}
	got, err := s.WorkItemByRef(ctx, "github:acme/app#7")
	if err != nil || got.ID != first.ID || len(got.Refs) != 3 {
		t.Errorf("lookup by reference = %+v, %v", got, err)
	}
}

func TestReferencesAreChecked(t *testing.T) {
	s, ctx := open(t), context.Background()
	a := actor(t, s)
	p, _, _ := s.UpsertProject(ctx, "default", "acme/app", 0, nil)
	for _, bad := range []string{"", "nocolon", "GitHub:x", "jira:", "jira:A B", "jira:ABC-1\n"} {
		if _, err := s.CreateWorkItem(ctx, p.ID, "t", a.ID, bad); err == nil {
			t.Errorf("%q accepted as a reference", bad)
		}
	}
}

func TestAWorkItemNeedsARealActorAndProject(t *testing.T) {
	s, ctx := open(t), context.Background()
	a := actor(t, s)
	p, _, _ := s.UpsertProject(ctx, "default", "acme/app", 0, nil)
	if _, err := s.CreateWorkItem(ctx, p.ID, "t", "ac_missing"); err == nil {
		t.Error("an unknown actor was accepted")
	}
	if _, err := s.CreateWorkItem(ctx, "pj_missing", "t", a.ID); err == nil {
		t.Error("an unknown project was accepted")
	}
}

func TestActorsAreChecked(t *testing.T) {
	s := open(t)
	if _, err := s.EnsureActor(context.Background(), Actor{Kind: "robot", DisplayName: "x"}); err == nil {
		t.Error("a kind other than human or bot was accepted")
	}
	if _, err := s.EnsureActor(context.Background(), Actor{Kind: "bot"}); err == nil {
		t.Error("an actor without a name was accepted")
	}
}

func TestAProjectWithWorkItemsIsNotDeleted(t *testing.T) {
	s, ctx := open(t), context.Background()
	a := actor(t, s)
	p, _, _ := s.UpsertProject(ctx, "default", "acme/app", 0, nil)
	empty, _, _ := s.UpsertProject(ctx, "default", "acme/empty", 1, nil)
	if _, err := s.CreateWorkItem(ctx, p.ID, "t", a.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteProject(ctx, p.ID); !errors.Is(err, ErrInUse) {
		t.Errorf("deleting a project with work items: %v", err)
	}
	if err := s.DeleteProject(ctx, empty.ID); err != nil {
		t.Errorf("deleting an unused project: %v", err)
	}
}

func TestTheSameIdentityIsTheSameActor(t *testing.T) {
	s, ctx := open(t), context.Background()
	first, err := s.EnsureActor(ctx, Actor{Kind: "human", DisplayName: "Ada", Identities: map[string]string{"github": "ada"}})
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.EnsureActor(ctx, Actor{Kind: "human", DisplayName: "Ada L.", Identities: map[string]string{"github": "ada"}})
	if err != nil || again.ID != first.ID || again.DisplayName != "Ada" {
		t.Errorf("a known identity gave %+v, %v, want the first actor unchanged", again, err)
	}
	other, _ := s.EnsureActor(ctx, Actor{Kind: "human", DisplayName: "Grace", Identities: map[string]string{"github": "grace"}})
	if other.ID == first.ID {
		t.Error("two logins share one actor")
	}
}
