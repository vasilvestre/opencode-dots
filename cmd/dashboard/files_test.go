package main

import (
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
