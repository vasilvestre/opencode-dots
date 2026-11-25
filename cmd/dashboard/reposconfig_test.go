package main

import (
	"strings"
	"testing"
)

const sampleRepos = `# header comment
version: 1
settings: {max_per_run: 6}
defaults:
  review:
    ignore_authors: ['bot[bot]']
profiles:
  managed:
    tend: {auto_merge: true}
    review: true
repos:
  # keeps this comment
  acme/managed:
    use: [managed]
    fix_issue: {label: dot}
  acme/plain:
    reply: true
  acme/empty: {}
`

func rowOf(t *testing.T, data string, name string) repoRow {
	t.Helper()
	cfg, err := buildReposConfig([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range cfg.Repos {
		if r.Name == name {
			return r
		}
	}
	t.Fatalf("no row %s", name)
	return repoRow{}
}

func patch(t *testing.T, data string, p reposPatch) string {
	t.Helper()
	out, err := applyReposPatch([]byte(data), p)
	if err != nil {
		t.Fatalf("patch %+v: %v", p, err)
	}
	return string(out)
}

func TestReposConfigReportsEffectiveState(t *testing.T) {
	m := rowOf(t, sampleRepos, "acme/managed")
	if !m.Features["review"].On || m.Features["review"].Source != "profile" {
		t.Fatalf("review should come from the profile: %+v", m.Features["review"])
	}
	if f := m.Features["fix_issue"]; !f.On || !f.Custom {
		t.Fatalf("fix_issue is a custom block: %+v", f)
	}
	if rowOf(t, sampleRepos, "acme/plain").Features["review"].On {
		t.Fatal("a default must not switch review on")
	}
}

func TestToggleOnAddsTrueAndKeepsComments(t *testing.T) {
	out := patch(t, sampleRepos, reposPatch{Repo: "acme/plain", Key: "review", Value: true})
	if !rowOf(t, out, "acme/plain").Features["review"].On {
		t.Fatal("review should be on")
	}
	for _, want := range []string{"# header comment", "# keeps this comment"} {
		if !strings.Contains(out, want) {
			t.Fatalf("lost %q:\n%s", want, out)
		}
	}
}

func TestToggleOffAProfileKeyWritesFalse(t *testing.T) {
	out := patch(t, sampleRepos, reposPatch{Repo: "acme/managed", Key: "review", Value: false})
	if rowOf(t, out, "acme/managed").Features["review"].On {
		t.Fatalf("review should be off:\n%s", out)
	}
	if !strings.Contains(out, "review: false") {
		t.Fatalf("expected an explicit false:\n%s", out)
	}
}

func TestToggleOffOwnKeyRemovesIt(t *testing.T) {
	out := patch(t, sampleRepos, reposPatch{Repo: "acme/plain", Key: "reply", Value: false})
	if rowOf(t, out, "acme/plain").Features["reply"].On {
		t.Fatal("reply should be off")
	}
}

func TestToggleOnKeepsExistingOptions(t *testing.T) {
	out := patch(t, sampleRepos, reposPatch{Repo: "acme/managed", Key: "fix_issue", Value: true})
	if out != patch(t, out, reposPatch{Repo: "acme/managed", Key: "fix_issue", Value: true}) {
		t.Fatal("a second toggle on must change nothing")
	}
	if !strings.Contains(out, "label: dot") {
		t.Fatalf("options were dropped:\n%s", out)
	}
}

func TestProviderSetAndClear(t *testing.T) {
	out := patch(t, sampleRepos, reposPatch{Repo: "acme/plain", Key: "provider", Value: "claude"})
	if r := rowOf(t, out, "acme/plain"); r.Provider != "claude" || r.ProviderSource != "repo" {
		t.Fatalf("provider not set: %+v", r)
	}
	out = patch(t, out, reposPatch{Repo: "acme/plain", Key: "provider", Value: nil})
	if rowOf(t, out, "acme/plain").Provider != "opencode" {
		t.Fatal("clearing should fall back to opencode")
	}
}

func TestAddAndRemoveRepo(t *testing.T) {
	out := patch(t, sampleRepos, reposPatch{Op: "add", Repo: "acme/new"})
	if r := rowOf(t, out, "acme/new"); r.Features["review"].On {
		t.Fatal("a new repository starts with everything off")
	}
	out = patch(t, out, reposPatch{Op: "remove", Repo: "acme/new"})
	if strings.Contains(out, "acme/new") {
		t.Fatal("repository not removed")
	}
	if _, err := applyReposPatch([]byte(sampleRepos), reposPatch{Op: "add", Repo: "acme/plain"}); err == nil {
		t.Fatal("adding a listed repository must fail")
	}
}

func TestPatchRefusesWhatTheTableDoesNotEdit(t *testing.T) {
	for _, p := range []reposPatch{
		{Repo: "acme/plain", Key: "checkout", Value: "/tmp/x"},
		{Repo: "acme/plain", Key: "provider", Value: "gemini"},
		{Repo: "acme/plain", Key: "review", Value: "yes"},
		{Repo: "../escape", Key: "review", Value: true},
		{Repo: "acme/missing", Key: "review", Value: true},
	} {
		if _, err := applyReposPatch([]byte(sampleRepos), p); err == nil {
			t.Fatalf("%+v should be refused", p)
		}
	}
}
