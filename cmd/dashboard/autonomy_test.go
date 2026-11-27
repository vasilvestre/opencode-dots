package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseGateFile(t *testing.T) {
	text := "action: comment\nref: acme/app#7\nautonomy: propose\nclass: 2\nartifact: /state/reviews/x/comment.md\nreason: autonomy propose\nstray: x\nreason: second\n"
	it, ok := parseGateFile("20261122T101500Z-comment-acme_app_7.txt", "held", text)
	if !ok {
		t.Fatal("not parsed")
	}
	if it.Action != "comment" || it.Ref != "acme/app#7" || it.Class != "2" || it.Reason != "autonomy propose" ||
		it.Artifact != "/state/reviews/x/comment.md" || it.At != "2026-11-22T10:15:00Z" {
		t.Fatalf("item = %+v", it)
	}
	none, _ := parseGateFile("20261122T101500Z-merge-x.txt", "notes", "artifact: none\n")
	if none.Artifact != "" {
		t.Fatalf("artifact none = %q", none.Artifact)
	}
	for _, bad := range []string{"notes.txt", "../20261122T101500Z-a-b.txt", "20261122T101500Z-a-b.md"} {
		if _, ok := parseGateFile(bad, "notes", ""); ok {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestResolveArtifact(t *testing.T) {
	base := t.TempDir()
	state := filepath.Join(base, "state")
	os.MkdirAll(filepath.Join(state, "reviews"), 0o755)
	os.WriteFile(filepath.Join(state, "reviews", "c.md"), []byte("hi"), 0o644)
	secret := filepath.Join(base, "secret.txt")
	os.WriteFile(secret, []byte("no"), 0o644)
	os.Symlink(secret, filepath.Join(state, "reviews", "out.md"))
	os.Symlink(base, filepath.Join(state, "up"))

	cases := []struct {
		name, in string
		ok       bool
	}{
		{"relative", "reviews/c.md", true},
		{"absolute inside", filepath.Join(state, "reviews", "c.md"), true},
		{"container prefix", "/state/reviews/c.md", true},
		{"empty", "", false},
		{"traversal", "reviews/../../secret.txt", false},
		{"absolute outside", secret, false},
		{"symlink file out", "reviews/out.md", false},
		{"symlink dir out", "up/secret.txt", false},
		{"missing", "reviews/none.md", false},
		{"directory", "reviews", false},
		{"nul", "reviews/c.md\x00", false},
	}
	for _, c := range cases {
		if _, ok := resolveArtifact(state, c.in); ok != c.ok {
			t.Errorf("%s: ok = %v, want %v", c.name, ok, c.ok)
		}
	}
}

func gateFixture(t *testing.T) *dash {
	state := t.TempDir()
	os.MkdirAll(filepath.Join(state, "notes"), 0o755)
	os.MkdirAll(filepath.Join(state, "held"), 0o755)
	os.WriteFile(filepath.Join(state, "c.md"), []byte("comment body"), 0o644)
	os.WriteFile(filepath.Join(state, "big.md"), []byte(strings.Repeat("x", autonomyArtifactCap+1)), 0o644)
	w := func(dir, name, art string) {
		body := "action: comment\nref: acme/app#1\nautonomy: propose\nclass: 1\nartifact: " + art + "\nreason: r\n"
		os.WriteFile(filepath.Join(state, dir, name), []byte(body), 0o644)
	}
	w("held", "20261122T100000Z-comment-a.txt", "c.md")
	w("held", "20261123T100000Z-comment-b.txt", "big.md")
	w("held", "20261124T100000Z-comment-c.txt", "../etc/passwd")
	w("notes", "20261122T100000Z-comment-d.txt", "none")
	os.WriteFile(filepath.Join(state, "held", "README"), []byte("x"), 0o644)
	return &dash{state: state, allow: map[string]bool{}}
}

func TestAutonomyListNewestFirst(t *testing.T) {
	d := gateFixture(t)
	rec := httptest.NewRecorder()
	d.autonomy(rec, httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8099/api/autonomy", nil))
	body := rec.Body.String()
	if rec.Code != 200 || strings.Index(body, "20261124") > strings.Index(body, "20261122T100000Z-comment-a") || strings.Contains(body, "README") {
		t.Fatalf("%d %s", rec.Code, body)
	}
	if !strings.Contains(body, `"readable":true`) || !strings.Contains(body, `"notes":[{`) {
		t.Fatalf("%s", body)
	}
}

func TestAutonomyArtifactEndpoint(t *testing.T) {
	d := gateFixture(t)
	get := func(host, q string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		d.autonomyArtifact(rec, httptest.NewRequest(http.MethodGet, "http://"+host+"/api/autonomy/artifact?"+q, nil))
		return rec
	}
	if r := get("127.0.0.1:8099", "kind=held&name=20261122T100000Z-comment-a.txt"); r.Code != 200 || r.Body.String() != "comment body" {
		t.Fatalf("ok case: %d %s", r.Code, r.Body.String())
	}
	if r := get("127.0.0.1:8099", "kind=held&name=20261123T100000Z-comment-b.txt"); r.Code != 413 {
		t.Fatalf("big: %d", r.Code)
	}
	if r := get("127.0.0.1:8099", "kind=held&name=20261124T100000Z-comment-c.txt"); r.Code != 404 {
		t.Fatalf("traversal: %d", r.Code)
	}
	if r := get("127.0.0.1:8099", "kind=held&name=../../etc/passwd"); r.Code != 400 {
		t.Fatalf("bad name: %d", r.Code)
	}
	if r := get("127.0.0.1:8099", "kind=state&name=20261122T100000Z-comment-a.txt"); r.Code != 400 {
		t.Fatalf("bad kind: %d", r.Code)
	}
	if r := get("evil.example:8099", "kind=held&name=20261122T100000Z-comment-a.txt"); r.Code != 403 {
		t.Fatalf("host: %d", r.Code)
	}
}

// The gate keeps a copy of each artifact under held/artifacts/<record>/ and the
// record points at it; the original path is on a source line.
func TestAutonomyArtifactCopyUnderHeld(t *testing.T) {
	d := gateFixture(t)
	dir := filepath.Join(d.state, "held", "artifacts", "20261125T100000Z-push-acme_app_2")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "held.patch"), []byte("patch body"), 0o644)
	rec := "action: push\nref: acme/app#2\nautonomy: propose\nclass: 1\nartifact: " + filepath.Join(dir, "held.patch") +
		"\nsource: /state/fix/acme/app/issue-2/held.patch\nreason: r\n"
	os.WriteFile(filepath.Join(d.state, "held", "20261125T100000Z-push-acme_app_2.txt"), []byte(rec), 0o644)

	list := httptest.NewRecorder()
	d.autonomy(list, httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8099/api/autonomy", nil))
	body := list.Body.String()
	if list.Code != 200 || !strings.Contains(body, "20261125T100000Z-push-acme_app_2.txt") || strings.Contains(body, `"name":"artifacts"`) {
		t.Fatalf("list: %d %s", list.Code, body)
	}
	i := strings.Index(body, "20261125T100000Z-push-acme_app_2.txt")
	if !strings.Contains(body[i:i+400], `"readable":true`) {
		t.Fatalf("copy not readable: %s", body)
	}
	rr := httptest.NewRecorder()
	d.autonomyArtifact(rr, httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8099/api/autonomy/artifact?kind=held&name=20261125T100000Z-push-acme_app_2.txt", nil))
	if rr.Code != 200 || rr.Body.String() != "patch body" {
		t.Fatalf("artifact: %d %s", rr.Code, rr.Body.String())
	}
}

// publish-held moves the artifact copy next to the released record and leaves the
// record's artifact line alone: the endpoint still finds it.
func TestAutonomyArtifactOfReleasedRecord(t *testing.T) {
	d := gateFixture(t)
	name := "20261125T100000Z-comment-acme_app_2.txt"
	old := filepath.Join(d.state, "held", "artifacts", name, "answer.md")
	dir := filepath.Join(d.state, "held", "released", "artifacts", name)
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "answer.md"), []byte("released body"), 0o644)
	rec := "action: comment\nref: acme/app#2\nautonomy: propose\nclass: 1\nartifact: " + old + "\nreason: r\n"
	os.WriteFile(filepath.Join(d.state, "held", "released", name), []byte(rec), 0o644)

	get := func(url string) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		d.autonomyArtifact(rr, httptest.NewRequest(http.MethodGet, url, nil))
		return rr
	}
	if rr := get("http://127.0.0.1:8099/api/autonomy/artifact?kind=released&name=" + name); rr.Code != 200 || rr.Body.String() != "released body" {
		t.Fatalf("released artifact: %d %s", rr.Code, rr.Body.String())
	}
	// A held lookup does not borrow the released copy.
	if rr := get("http://127.0.0.1:8099/api/autonomy/artifact?kind=held&name=" + name); rr.Code != 404 {
		t.Fatalf("held kind: %d", rr.Code)
	}
	list := httptest.NewRecorder()
	d.autonomy(list, httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8099/api/autonomy", nil))
	if !strings.Contains(list.Body.String(), `"readable":true`) {
		t.Fatalf("released item not readable: %s", list.Body.String())
	}
}
