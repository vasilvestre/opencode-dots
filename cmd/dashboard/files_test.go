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

func TestResolveEditable(t *testing.T) {
	d := &dash{root: "/repo"}
	ok := []string{
		"workflows/review-pr.yaml",
		"workflows/a_b-9.yaml",
		"./workflows/reply-pr.yaml",
		"awf-gh.yaml",
		"awf-gh.workstation.yaml",
		"tend.list",
		"repos.map",
	}
	for _, rel := range ok {
		if _, got := d.resolveEditable(rel); !got {
			t.Errorf("refused %q, want accepted", rel)
		}
	}
	bad := []string{
		"",
		"workflows/../secrets.yaml",
		"../awf-gh.yaml",
		"workflows/nested/x.yaml",
		"workflows/x.yml",
		"workflows/.hidden.yaml",
		"state/logs/x.jsonl",
		"/etc/passwd",
		"awf-gh.yaml.bak",
		"awf-gh.local.example.yaml",
		"awf-gh..yaml",
		"awf-gh.Bad.yaml",
	}
	for _, rel := range bad {
		if abs, got := d.resolveEditable(rel); got {
			t.Errorf("accepted %q as %q, want refused", rel, abs)
		}
	}
}

func TestSaveFile(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "workflows"), 0o755)
	target := filepath.Join(root, "workflows", "review-pr.yaml")
	os.WriteFile(target, []byte("name: old\n"), 0o644)
	d := &dash{root: root}

	save := func(body, origin string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPut, "/api/save", strings.NewReader(body))
		req.Host = "127.0.0.1:8099"
		req.Header.Set("Content-Type", "application/json")
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		rec := httptest.NewRecorder()
		d.saveFile(rec, req)
		return rec
	}

	if rec := save(`{"path":"workflows/review-pr.yaml","content":"name: new\nversion: 2\n"}`, "http://127.0.0.1:8099"); rec.Code != 200 {
		t.Fatalf("save returned %d: %s", rec.Code, rec.Body)
	}
	if got, _ := os.ReadFile(target); string(got) != "name: new\nversion: 2\n" {
		t.Fatalf("file holds %q", got)
	}
	backups, _ := filepath.Glob(target + ".*.bak")
	if len(backups) != 1 {
		t.Fatalf("want one backup, got %v", backups)
	}
	if old, _ := os.ReadFile(backups[0]); string(old) != "name: old\n" {
		t.Fatalf("backup holds %q", old)
	}

	// Invalid YAML is refused and the disk keeps the last good content.
	if rec := save(`{"path":"workflows/review-pr.yaml","content":"name: [unclosed\n"}`, "http://127.0.0.1:8099"); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid YAML returned %d, want 422", rec.Code)
	}
	if got, _ := os.ReadFile(target); string(got) != "name: new\nversion: 2\n" {
		t.Fatalf("invalid YAML reached the disk: %q", got)
	}

	if rec := save(`{"path":"workflows/review-pr.yaml","content":"name: x\n"}`, "http://evil.example"); rec.Code != http.StatusForbidden {
		t.Fatalf("foreign origin returned %d, want 403", rec.Code)
	}
	if rec := save(`{"path":"/etc/passwd","content":"x"}`, "http://127.0.0.1:8099"); rec.Code != http.StatusForbidden {
		t.Fatalf("foreign path returned %d, want 403", rec.Code)
	}
	if rec := save(`{"path":"tend.list","content":"x"}`, "http://127.0.0.1:8099"); rec.Code != 200 {
		t.Fatalf("plain list save returned %d", rec.Code)
	}
}

func saveTo(t *testing.T, d *dash, path, content string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"path": path, "content": content})
	req := httptest.NewRequest(http.MethodPut, "/api/save", strings.NewReader(string(body)))
	req.Host = "127.0.0.1:8099"
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	d.saveFile(rec, req)
	return rec
}

// repos.yaml is the one file to edit. Saving it writes the files the other tools
// read, and a repos.yaml that cannot generate never reaches the disk.
func TestSavingReposYamlRegeneratesTheOtherFiles(t *testing.T) {
	root := t.TempDir()
	d := &dash{root: root}
	good := "version: 1\nrepos:\n  a/b: {review: true, tend: true, checkout: /src/b}\n"

	rec := saveTo(t, d, "repos.yaml", good)
	if rec.Code != 200 {
		t.Fatalf("save returned %d: %s", rec.Code, rec.Body)
	}
	var resp struct {
		Generated []string `json:"generated"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || len(resp.Generated) != 3 {
		t.Fatalf("response = %s, want the three generated files", rec.Body)
	}
	for file, want := range map[string]string{"tend.list": "a/b false", "repos.map": "a/b=/src/b", "awf-gh.yaml": "workflow: review-pr"} {
		if got, _ := os.ReadFile(filepath.Join(root, file)); !strings.Contains(string(got), want) {
			t.Errorf("%s = %q, want it to contain %q", file, got, want)
		}
	}

	bad := saveTo(t, d, "repos.yaml", "version: 1\nrepos:\n  a/b: {reveiw: true}\n")
	if bad.Code != http.StatusUnprocessableEntity || !strings.Contains(bad.Body.String(), `unknown key \"reveiw\"`) {
		t.Fatalf("typo returned %d: %s, want 422 naming the key", bad.Code, bad.Body)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "repos.yaml")); string(got) != good {
		t.Fatalf("a repos.yaml that cannot generate reached the disk: %q", got)
	}
}

func TestGeneratedFilesAreProtectedOnlyOnceReposYamlExists(t *testing.T) {
	root := t.TempDir()
	d := &dash{root: root}
	if rec := saveTo(t, d, "tend.list", "a/b true\n"); rec.Code != 200 {
		t.Fatalf("before the migration a hand edit returned %d, want 200", rec.Code)
	}
	if err := os.WriteFile(filepath.Join(root, "repos.yaml"), []byte("version: 1\nrepos:\n  a/b: {review: true}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"tend.list", "awf-gh.yaml", "repos.map", "sync.map"} {
		rec := saveTo(t, d, file, "x\n")
		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "edit repos.yaml") {
			t.Errorf("%s: hand edit returned %d: %s, want 409 pointing at repos.yaml", file, rec.Code, rec.Body)
		}
	}

	rec := httptest.NewRecorder()
	d.files(rec, httptest.NewRequest(http.MethodGet, "/api/files", nil))
	var list []editableFile
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	flags := map[string]bool{}
	for _, f := range list {
		flags[f.Path] = f.Generated
	}
	if v, ok := flags["repos.yaml"]; !ok || v {
		t.Errorf("repos.yaml listed as %v/%v, want editable", ok, v)
	}
	for _, name := range []string{"awf-gh.yaml", "tend.list", "repos.map", "sync.map"} {
		if !flags[name] {
			t.Errorf("%s is not flagged generated", name)
		}
	}
}

func TestOptionalGeneratedFilesAreNotReportedAsProblemsAndExecutorFilesAreListed(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "repos.yaml"), []byte("version: 1\nrepos:\n  a/b: {tend: true}\n"), 0o644)
	os.WriteFile(filepath.Join(root, "awf-gh.workstation.yaml"), []byte("repos: []\n"), 0o644)
	os.WriteFile(filepath.Join(root, "awf-gh.local.example.yaml"), []byte("repos: []\n"), 0o644)
	d := &dash{root: root}
	rec := httptest.NewRecorder()
	d.files(rec, httptest.NewRequest(http.MethodGet, "/api/files", nil))
	var list []editableFile
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	byPath := map[string]editableFile{}
	for _, f := range list {
		byPath[f.Path] = f
	}
	for _, name := range []string{"awf-gh.yaml", "sync.map"} {
		if f := byPath[name]; f.Exists || !f.Optional || !f.Generated {
			t.Errorf("%s = %+v, want a generated, optional file that is absent", name, f)
		}
	}
	for _, name := range []string{"tend.list", "repos.map"} {
		if f := byPath[name]; f.Optional {
			t.Errorf("%s is flagged optional: its absence is a problem", name)
		}
	}
	if f, ok := byPath["awf-gh.workstation.yaml"]; !ok || !f.Exists || !f.Generated || f.Kind != "watcher" {
		t.Errorf("the executor file is not listed as a generated watcher file: %+v (listed: %v)", f, ok)
	}
	if _, ok := byPath["awf-gh.local.example.yaml"]; ok {
		t.Error("an example file was listed as a generated watcher file")
	}
	if rec := saveTo(t, d, "awf-gh.workstation.yaml", "x\n"); rec.Code != http.StatusConflict {
		t.Errorf("a hand edit of the executor file returned %d, want 409", rec.Code)
	}
}
