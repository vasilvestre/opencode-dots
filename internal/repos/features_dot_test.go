package repos

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const dotRepos = `repos:
  acme/mine:
    dot: alpha
    tend: true
    review: true
    checkout: /w/mine
    sync: true
    autonomy: auto
    circuit_breaker: true
  acme/theirs:
    dot: beta
    tend: true
    review: true
    checkout: /w/theirs
    sync: true
    autonomy: auto
    circuit_breaker: true
  acme/plain:
    tend: true
    review: true
`

func dotBuild(t *testing.T, settings, body string) (map[string][]byte, error) {
	t.Helper()
	return Build([]byte("version: 1\n"+settings+body), t.TempDir(), Settings{})
}

func TestDotNeedsADotID(t *testing.T) {
	_, err := dotBuild(t, "", dotRepos)
	if err == nil || !strings.Contains(err.Error(), "settings.dot_id") {
		t.Fatalf("want an error naming settings.dot_id, got %v", err)
	}
}

func TestForeignRepositoryIsInEveryFileOnlyAsACount(t *testing.T) {
	files, err := dotBuild(t, "settings:\n  dot_id: alpha\n", dotRepos)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{FileAwfGh, FileTend, FileCheckout, FileSync, FileBreaker} {
		if _, ok := files[name]; !ok {
			t.Fatalf("%s not generated", name)
		}
	}
	for name, body := range files {
		s := string(body)
		if strings.Contains(s, "acme/theirs") || strings.Contains(s, "/w/theirs") {
			t.Errorf("%s still mentions the foreign repository:\n%s", name, s)
		}
		if !strings.Contains(s, "# skipped: 1 repository owned by another dot\n") {
			t.Errorf("%s lacks the skipped count:\n%s", name, s)
		}
	}
	for _, name := range []string{FileAwfGh, FileTend, FileCheckout, FileSync, FileBreaker} {
		if !strings.Contains(string(files[name]), "acme/mine") && !strings.Contains(string(files[name]), "/w/mine") {
			t.Errorf("%s lost the own repository", name)
		}
	}
	if !strings.Contains(string(files[FileTend]), "acme/plain") {
		t.Error("a repository without the key must stay")
	}
}

func TestOwnRepositoryIsGeneratedAsWithoutTheKey(t *testing.T) {
	with, err := dotBuild(t, "settings:\n  dot_id: alpha\n", "repos:\n  acme/a:\n    dot: alpha\n    tend: true\n    review: true\n    sync: /w/a\n")
	if err != nil {
		t.Fatal(err)
	}
	without, err := dotBuild(t, "", "repos:\n  acme/a:\n    tend: true\n    review: true\n    sync: /w/a\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(with) != len(without) {
		t.Fatalf("file sets differ: %d vs %d", len(with), len(without))
	}
	for name, body := range without {
		if !bytes.Equal(with[name], body) {
			t.Errorf("%s differs", name)
		}
	}
}

func TestLegacyConfigurationIsUnchanged(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "repos.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := Build(data, t.TempDir(), Settings{})
	if err != nil {
		t.Fatal(err)
	}
	named, err := Build(bytes.Replace(data, []byte("settings:\n"), []byte("settings:\n  dot_id: alpha\n"), 1), t.TempDir(), Settings{})
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range legacy {
		if strings.Contains(string(body), "skipped") {
			t.Errorf("%s has a skipped line without a dot key", name)
		}
		if !bytes.Equal(named[name], body) {
			t.Errorf("%s changes when only dot_id is set", name)
		}
	}
}

func TestDotFromDefaultsAndProfiles(t *testing.T) {
	files, err := dotBuild(t, "settings:\n  dot_id: alpha\n", `defaults:
  dot: beta
profiles:
  mine:
    dot: alpha
repos:
  acme/default-foreign: {tend: true}
  acme/profile-own: {use: [mine], tend: true}
  acme/override-own: {dot: alpha, tend: true}
`)
	if err != nil {
		t.Fatal(err)
	}
	tend := string(files[FileTend])
	if strings.Contains(tend, "default-foreign") || !strings.Contains(tend, "profile-own") || !strings.Contains(tend, "override-own") {
		t.Fatalf("tend.list:\n%s", tend)
	}
	if !strings.Contains(tend, "# skipped: 1 repository owned") {
		t.Fatalf("count missing:\n%s", tend)
	}
}

func TestEveryRepositoryForeignWritesNoWatcherFile(t *testing.T) {
	files, err := dotBuild(t, "settings:\n  dot_id: alpha\n", "repos:\n  acme/x: {dot: beta, review: true}\n  acme/y: {dot: beta, review: true}\n")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := files[FileAwfGh]; ok {
		t.Error("awf-gh.yaml written for nothing")
	}
	if !strings.Contains(string(files[FileTend]), "# skipped: 2 repositories owned by another dot") {
		t.Errorf("tend.list:\n%s", files[FileTend])
	}
}

func TestForeignRepositoryDropsItsExecutorFile(t *testing.T) {
	files, err := dotBuild(t, "settings:\n  dot_id: alpha\n  executors:\n    pc: http://127.0.0.1:2512\n",
		"repos:\n  acme/x: {dot: beta, review: true, executor: pc}\n  acme/y: {review: true}\n")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := files[ExecutorFile("pc")]; ok {
		t.Error("executor watcher file written for a foreign repository")
	}
}

func TestBadDotIDsAreRefused(t *testing.T) {
	for _, id := range []string{"Alpha", "a b", "a/b", strings.Repeat("a", 41)} {
		if _, err := dotBuild(t, "settings:\n  dot_id: \""+id+"\"\n", "repos:\n  acme/x: {tend: true}\n"); err == nil {
			t.Errorf("dot_id %q accepted", id)
		}
		if _, err := dotBuild(t, "settings:\n  dot_id: alpha\n", "repos:\n  acme/x: {tend: true, dot: \""+id+"\"}\n"); err == nil {
			t.Errorf("dot %q accepted", id)
		}
	}
	if _, err := dotBuild(t, "settings:\n  dot_id: alpha\n", "repos:\n  acme/x: {tend: true, dot: 3}\n"); err == nil {
		t.Error("a number accepted as a dot")
	}
	if _, err := dotBuild(t, "settings:\n  dot_id: a_b-1\n", "repos:\n  acme/x: {tend: true, dot: z9}\n"); err != nil {
		t.Errorf("valid ids refused: %v", err)
	}
}

func TestDotIsListedWithADoc(t *testing.T) {
	for _, line := range FeatureDocs() {
		if strings.HasPrefix(line, "dot ") && strings.Contains(line, "dot_id") {
			return
		}
	}
	t.Fatal("bin/repos features does not list dot")
}
