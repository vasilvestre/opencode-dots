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

func TestRulesAreReportedAsAList(t *testing.T) {
	cfg, err := buildReposConfig([]byte(sampleRepos))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Forms["review"].Label || !cfg.Forms["fix_issue"].Label {
		t.Fatalf("only fix_issue takes a label: %+v", cfg.Forms)
	}
	if len(cfg.Forms["review"].DefaultIgnore) != 1 {
		t.Fatalf("the default ignore list should reach the form: %+v", cfg.Forms["review"])
	}
	var managed repoRow
	for _, r := range cfg.Repos {
		if r.Name == "acme/managed" {
			managed = r
		}
	}
	if got := managed.Features["review"].Rules; len(got) != 1 {
		t.Fatalf("a profile's `true` is one empty rule: %v", got)
	}
	if managed.Features["tend"].AutoMerge == nil || !*managed.Features["tend"].AutoMerge {
		t.Fatalf("tend auto_merge comes from the profile: %+v", managed.Features["tend"])
	}
}

func TestSetRulesWritesTheShortestForm(t *testing.T) {
	one := patch(t, sampleRepos, reposPatch{Repo: "acme/plain", Key: "rules", Feature: "reply", Value: []any{map[string]any{}}})
	if !strings.Contains(one, "reply: true") {
		t.Fatalf("one empty rule is `true`:\n%s", one)
	}
	two := patch(t, sampleRepos, reposPatch{Repo: "acme/plain", Key: "rules", Feature: "reply", Value: []any{
		map[string]any{},
		map[string]any{"on": []any{"command"}, "only_authors": []any{"me"}, "ignore_authors": []any{}},
	}})
	for _, want := range []string{`"on": [command]`, "only_authors: [me]", "ignore_authors: []"} {
		if !strings.Contains(two, want) {
			t.Fatalf("missing %q:\n%s", want, two)
		}
	}
	if r := rowOf(t, two, "acme/plain").Features["reply"]; len(r.Rules) != 2 {
		t.Fatalf("two rules expected: %+v", r)
	}
}

func TestSetRulesRefusesWhatIsNotARuleFeature(t *testing.T) {
	for _, p := range []reposPatch{
		{Repo: "acme/plain", Key: "rules", Feature: "tend", Value: []any{map[string]any{}}},
		{Repo: "acme/plain", Key: "rules", Feature: "reply", Value: []any{}},
		{Repo: "acme/plain", Key: "rules", Feature: "reply", Value: "x"},
	} {
		if _, err := applyReposPatch([]byte(sampleRepos), p); err == nil {
			t.Fatalf("%+v should be refused", p)
		}
	}
}

func TestAutoMerge(t *testing.T) {
	if _, err := applyReposPatch([]byte(sampleRepos), reposPatch{Repo: "acme/plain", Key: "auto_merge", Value: true}); err == nil {
		t.Fatal("auto_merge must not switch tend on")
	}
	withTend := patch(t, patch(t, sampleRepos, reposPatch{Repo: "acme/plain", Key: "tend", Value: true}),
		reposPatch{Repo: "acme/plain", Key: "auto_merge", Value: true})
	if a := rowOf(t, withTend, "acme/plain").Features["tend"].AutoMerge; a == nil || !*a {
		t.Fatalf("auto_merge not set:\n%s", withTend)
	}
	off := patch(t, withTend, reposPatch{Repo: "acme/plain", Key: "auto_merge", Value: false})
	if a := rowOf(t, off, "acme/plain").Features["tend"]; !a.On || (a.AutoMerge != nil && *a.AutoMerge) {
		t.Fatalf("tend stays on, auto_merge goes off: %+v\n%s", a, off)
	}
	// Inherited from a profile: only an explicit false switches it off.
	inherited := patch(t, sampleRepos, reposPatch{Repo: "acme/managed", Key: "auto_merge", Value: false})
	if a := rowOf(t, inherited, "acme/managed").Features["tend"].AutoMerge; a == nil || *a {
		t.Fatalf("the profile's auto_merge must be overridden:\n%s", inherited)
	}
	if _, err := applyReposPatch([]byte(sampleRepos), reposPatch{Repo: "acme/plain", Key: "auto_merge", Value: "yes"}); err == nil {
		t.Fatal("a non boolean auto_merge must be refused")
	}
}

// An edit from the dashboard must leave what it does not show alone: the executors of a
// server (ADR 0031) and the `executor` key of a repository.
const executorRepos = `version: 1
settings:
  executors:
    pc: http://127.0.0.1:2512
repos:
  acme/remote:
    review: true
    executor: pc
  acme/local:
    tend: true
`

func TestEditsKeepTheExecutorsOfAServer(t *testing.T) {
	for name, p := range map[string]reposPatch{
		"a toggle on another repository":    {Repo: "acme/local", Key: "reply", Value: true},
		"a toggle on the repository itself": {Repo: "acme/remote", Key: "reply", Value: true},
		"adding a repository":               {Op: "add", Repo: "acme/new"},
		"removing the other repository":     {Op: "remove", Repo: "acme/local"},
	} {
		t.Run(name, func(t *testing.T) {
			out := patch(t, executorRepos, p)
			for _, want := range []string{"pc: http://127.0.0.1:2512", "executor: pc"} {
				if !strings.Contains(out, want) {
					t.Errorf("%q lost after the edit:\n%s", want, out)
				}
			}
			if _, err := buildReposConfig([]byte(out)); err != nil {
				t.Errorf("the edited file no longer builds: %v\n%s", err, out)
			}
		})
	}
}

func TestADocumentWithExecutorsIsReadable(t *testing.T) {
	r := rowOf(t, executorRepos, "acme/remote")
	if !r.Features["review"].On {
		t.Errorf("review should be on: %+v", r.Features["review"])
	}
}

func TestReposConfigShowsTheTrustLevel(t *testing.T) {
	const data = `version: 1
settings:
  trust: {host: client, allow: {providers: [opencode], executors: [server]}}
  executors: {server: http://127.0.0.1:2512}
repos:
  acme/client:
    trust: client
    executor: server
  acme/open: {}
`
	if got := rowOf(t, data, "acme/client").Trust; got != "client" {
		t.Fatalf("trust = %q, want client", got)
	}
	if got := rowOf(t, data, "acme/open").Trust; got != "" {
		t.Fatalf("trust = %q, want empty for an open repository", got)
	}
}
