package repos

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"opencode-dots/internal/store"
)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "dots.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// fixtureDocs are repos.yaml documents of several shapes: every watcher feature with a checkout,
// a sync and a provider, an executor document and a tend-only one.
func fixtureDocs(t *testing.T) map[string][]byte {
	t.Helper()
	const full = `version: 1
settings:
  max_per_run: 6
repos:
  acme/app:
    checkout: /home/me/src/app
    sync: true
    provider: claude
    tend: true
    review: true
    reply: true
    fix_ci: true
    fix_issue: {label: dot}
  acme/lib:
    checkout: /home/me/src/lib
    sync: /home/me/src/lib
    review: {on: [pr-opened]}
`
	return map[string][]byte{"full": []byte(full), "executors": []byte(executorDoc), "tend only": []byte("version: 1\nrepos:\n  acme/a: {tend: true}\n  acme/b: {tend: {auto_merge: true}}\n")}
}

func TestStoreGeneratesTheSameFilesAsTheFile(t *testing.T) {
	for name, data := range fixtureDocs(t) {
		t.Run(name, func(t *testing.T) {
			ctx, st := context.Background(), openStore(t)
			if _, err := ImportToStore(ctx, st, "default", data, false); err != nil {
				t.Fatal(err)
			}
			if differ, err := DiffStoreAndFile(ctx, st, "default", data, t.TempDir()); err != nil || len(differ) != 0 {
				t.Fatalf("generated files differ: %v, %v", differ, err)
			}
		})
	}
}

func TestExportThenImportGivesTheSameDatabase(t *testing.T) {
	for name, data := range fixtureDocs(t) {
		t.Run(name, func(t *testing.T) {
			ctx, first := context.Background(), openStore(t)
			if _, err := ImportToStore(ctx, first, "default", data, false); err != nil {
				t.Fatal(err)
			}
			exported, err := ExportYAML(ctx, first, "default")
			if err != nil {
				t.Fatal(err)
			}
			second := openStore(t)
			if _, err := ImportToStore(ctx, second, "default", exported, false); err != nil {
				t.Fatalf("the export does not import: %v\n%s", err, exported)
			}
			a, _ := first.Projects(ctx, "default")
			b, _ := second.Projects(ctx, "default")
			if len(a) != len(b) {
				t.Fatalf("project count %d vs %d\n%s", len(a), len(b), exported)
			}
			for i := range a {
				if a[i].Repo != b[i].Repo || !reflect.DeepEqual(a[i].Features, b[i].Features) {
					t.Errorf("project %s differs after export and import:\n%v\n%v", a[i].Repo, a[i].Features, b[i].Features)
				}
			}
			sa, _ := first.WorkspaceSettings(ctx, "default")
			sb, _ := second.WorkspaceSettings(ctx, "default")
			if string(sa) != string(sb) {
				t.Errorf("settings differ: %s vs %s", sa, sb)
			}
		})
	}
}

func TestImportReportsWhatItDid(t *testing.T) {
	ctx, st := context.Background(), openStore(t)
	src := []byte("version: 1\nrepos:\n  acme/a: {tend: true}\n  acme/b: {tend: true}\n")
	if res, err := ImportToStore(ctx, st, "default", src, false); err != nil || res.Added != 2 || res.Updated != 0 || res.Unchanged != 0 {
		t.Fatalf("first import = %+v, %v", res, err)
	}
	if res, _ := ImportToStore(ctx, st, "default", src, false); res.Unchanged != 2 || res.Added != 0 {
		t.Errorf("same file again = %+v, want nothing new", res)
	}
	changed := []byte("version: 1\nrepos:\n  acme/a: {tend: true, review: true}\n  acme/b: {tend: true}\n")
	if res, _ := ImportToStore(ctx, st, "default", changed, false); res.Updated != 1 || res.Unchanged != 1 {
		t.Errorf("one changed = %+v", res)
	}
}

func TestImportDeletesNothingUnlessAsked(t *testing.T) {
	ctx, st := context.Background(), openStore(t)
	two := []byte("version: 1\nrepos:\n  acme/a: {tend: true}\n  acme/b: {tend: true}\n")
	one := []byte("version: 1\nrepos:\n  acme/a: {tend: true}\n")
	ImportToStore(ctx, st, "default", two, false)
	if _, err := ImportToStore(ctx, st, "default", one, false); err != nil {
		t.Fatal(err)
	}
	if ps, _ := st.Projects(ctx, "default"); len(ps) != 2 {
		t.Fatalf("a file that omits a project removed it without --prune: %d left", len(ps))
	}
	res, err := ImportToStore(ctx, st, "default", one, true)
	if err != nil || len(res.Pruned) != 1 || res.Pruned[0] != "acme/b" {
		t.Fatalf("prune = %+v, %v", res, err)
	}
}

func TestPruneKeepsAProjectThatHasWorkItems(t *testing.T) {
	ctx, st := context.Background(), openStore(t)
	two := []byte("version: 1\nrepos:\n  acme/a: {tend: true}\n  acme/b: {tend: true}\n")
	ImportToStore(ctx, st, "default", two, false)
	who, _ := st.EnsureActor(ctx, store.Actor{Kind: "human", DisplayName: "Ada"})
	bs, _ := st.Projects(ctx, "default", "acme/b")
	b := bs[0]
	if _, err := st.CreateWorkItem(ctx, b.ID, "t", who.ID); err != nil {
		t.Fatal(err)
	}
	res, err := ImportToStore(ctx, st, "default", []byte("version: 1\nrepos:\n  acme/a: {tend: true}\n"), true)
	if err != nil || len(res.Pruned) != 0 || len(res.Kept) != 1 || res.Kept[0] != "acme/b" {
		t.Fatalf("prune = %+v, %v, want acme/b kept for its work items", res, err)
	}
}

func TestAFileThatDoesNotGenerateIsNotImported(t *testing.T) {
	ctx, st := context.Background(), openStore(t)
	_, err := ImportToStore(ctx, st, "default", []byte("version: 1\nrepos:\n  acme/a: {review: true, executor: nowhere}\n"), false)
	if err == nil || !strings.Contains(err.Error(), "nothing imported") {
		t.Fatalf("error = %v, want the import refused", err)
	}
	if ps, _ := st.Projects(ctx, "default"); len(ps) != 0 {
		t.Errorf("a refused import left %d projects", len(ps))
	}
}

func TestDiffNamesTheFilesThatDiffer(t *testing.T) {
	ctx, st := context.Background(), openStore(t)
	ImportToStore(ctx, st, "default", []byte("version: 1\nrepos:\n  acme/a: {tend: true}\n"), false)
	edited := []byte("version: 1\nrepos:\n  acme/a: {tend: {auto_merge: true}}\n")
	differ, err := DiffStoreAndFile(ctx, st, "default", edited, t.TempDir())
	if err != nil || !reflect.DeepEqual(differ, []string{FileTend}) {
		t.Errorf("differ = %v, %v, want only tend.list", differ, err)
	}
}
