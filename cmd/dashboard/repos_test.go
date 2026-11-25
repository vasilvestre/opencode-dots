package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The run dialog offers every repository the machine already knows, from the
// watcher config, the daily list, the path map and the state directories.
func TestRepoNames(t *testing.T) {
	root, state := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(root, "awf-gh.yaml"), []byte("repos:\n  - repo: owner/watched\n    workflow: review-pr\n"), 0o644)
	os.WriteFile(filepath.Join(root, "tend.list"), []byte("# comment\nowner/tended true\nnot a repo\n"), 0o644)
	os.WriteFile(filepath.Join(root, "repos.map"), []byte("owner/mapped=/tmp/x\n"), 0o644)
	os.MkdirAll(filepath.Join(state, "tend", "owner", "learned"), 0o755)

	d := &dash{root: root, state: state, awfgh: filepath.Join(root, "awf-gh.yaml")}
	rec := httptest.NewRecorder()
	d.repoNames(rec, httptest.NewRequest(http.MethodGet, "/api/repos/names", nil))

	var got []string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("api/repos/names returned %q: %v", rec.Body.String(), err)
	}
	want := []string{"owner/learned", "owner/mapped", "owner/tended", "owner/watched"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("api/repos/names returned %v, want %v", got, want)
	}
}

// Tower indexes every non-hidden file without a .towerignore, so the Repos tab
// has to say when the file is missing or holds nothing.
func TestReposTowerWarning(t *testing.T) {
	dir := t.TempDir()
	checkout := filepath.Join(dir, "checkout")
	if err := os.MkdirAll(checkout, 0o755); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	awfgh := filepath.Join(root, "awf-gh.yaml")
	os.WriteFile(awfgh, []byte("repos:\n  - repo: o/watched\n    workflow: review-pr\n"), 0o644)
	os.WriteFile(filepath.Join(root, "repos.map"), []byte("o/watched="+checkout+"\n"), 0o644)
	opencode := filepath.Join(dir, "opencode.jsonc")
	os.WriteFile(opencode, []byte(`{"mcp":{"servers":{"tower":{"type":"local","disabled":false}}}}`), 0o644)

	d := &dash{state: dir, root: root, awfgh: awfgh, opencode: opencode}
	read := func() watchedRepo {
		rec := httptest.NewRecorder()
		d.repos(rec, httptest.NewRequest(http.MethodGet, "/api/repos", nil))
		var out []watchedRepo
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("api/repos returned %q (err %v)", rec.Body.String(), err)
		}
		if len(out) != 1 {
			t.Fatalf("api/repos returned %d repositories, want 1", len(out))
		}
		return out[0]
	}

	if got := read().TowerIgnore; got != "missing" {
		t.Fatalf("tower_ignore = %q with no file, want missing", got)
	}

	// A file with only a comment lists no pattern.
	os.WriteFile(filepath.Join(checkout, ".towerignore"), []byte("# nothing yet\n\n"), 0o644)
	if got := read().TowerIgnore; got != "empty" {
		t.Fatalf("tower_ignore = %q for a comment-only file, want empty", got)
	}

	os.WriteFile(filepath.Join(checkout, ".towerignore"), []byte("state/\nbin/\n"), 0o644)
	if got := read().TowerIgnore; got != "" {
		t.Fatalf("tower_ignore = %q for a filled file, want none", got)
	}

	// Tower off means no warning at all.
	os.WriteFile(opencode, []byte(`{"mcp":{"servers":{"tower":{"disabled":true}}}}`), 0o644)
	os.WriteFile(filepath.Join(checkout, ".towerignore"), nil, 0o644)
	if got := read().TowerIgnore; got != "" {
		t.Fatalf("tower_ignore = %q with tower disabled, want none", got)
	}
}

func TestRepoNameRE(t *testing.T) {
	for _, ok := range []string{"owner/repo", "o/.github", "a.b/c-d_e"} {
		if !repoNameRE.MatchString(ok) {
			t.Errorf("%q rejected", ok)
		}
	}
	for _, bad := range []string{"../..", "a/..", "./x", "x/.", "a/b/c", "", "/x"} {
		if repoNameRE.MatchString(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}
