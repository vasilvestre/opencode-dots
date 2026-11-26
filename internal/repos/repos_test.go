package repos

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func build(t *testing.T, src string) *Output {
	t.Helper()
	doc, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	out, err := Generate(doc)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func buildErr(t *testing.T, src string) string {
	t.Helper()
	doc, err := Parse([]byte(src))
	if err != nil {
		return err.Error()
	}
	if _, err := Generate(doc); err != nil {
		return err.Error()
	}
	t.Fatalf("expected an error for:\n%s", src)
	return ""
}

func TestParseRefusesBadFiles(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"empty", "", "empty"},
		{"unknown top key", "version: 1\nrepoz: {}\n", `unknown key "repoz"`},
		{"wrong version", "version: 2\nrepos: {}\n", "version must be 1"},
		{"no repos", "version: 1\n", "missing repos"},
		{"bad name", "version: 1\nrepos:\n  nope: {}\n", "not an owner/repo name"},
		{"duplicate repo", "version: 1\nrepos:\n  a/b: {}\n  a/b: {}\n", "appears twice"},
		{"two documents", "version: 1\nrepos: {}\n---\nversion: 1\n", "one YAML document"},
		{"unknown setting", "version: 1\nsettings: {awf_urls: x}\nrepos: {}\n", "settings"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := buildErr(t, c.src); !strings.Contains(got, c.want) {
				t.Fatalf("error = %q, want it to contain %q", got, c.want)
			}
		})
	}
}

func TestUnknownKeyNamesTheKnownOnes(t *testing.T) {
	got := buildErr(t, "version: 1\nrepos:\n  a/b:\n    reveiw: true\n")
	for _, want := range []string{`a/b: unknown key "reveiw"`, "review", "fix_issue", "checkout"} {
		if !strings.Contains(got, want) {
			t.Errorf("error %q lacks %q", got, want)
		}
	}
}

// A default supplies values. It must never switch a feature on for a repository
// that did not ask for it, or one default line would add a rule everywhere.
func TestDefaultsNeverEnableAFeature(t *testing.T) {
	out := build(t, `version: 1
defaults:
  review: {ignore_authors: [bot]}
  tend: {auto_merge: true}
repos:
  a/with: {review: true, tend: true}
  a/without: {fix_issue: true}
`)
	if len(out.Rules) != 2 || out.Rules[0].Repo != "a/with" || out.Rules[0].Workflow != "review-pr" {
		t.Fatalf("rules = %+v, want one review-pr for a/with and one fix-issue for a/without", out.Rules)
	}
	if !reflect.DeepEqual(out.Rules[0].IgnoreAuthors, []string{"bot"}) {
		t.Errorf("review kept no default: %+v", out.Rules[0])
	}
	if len(out.Tend) != 1 || out.Tend[0].Repo != "a/with" || !out.Tend[0].AutoMerge {
		t.Errorf("tend = %+v, want only a/with with auto_merge", out.Tend)
	}
}

func TestOwnBlockOverridesAndFalseSwitchesOff(t *testing.T) {
	out := build(t, `version: 1
defaults:
  review: {ignore_authors: [bot], only_authors: [me]}
profiles:
  managed: {review: true, fix_issue: true, tend: true}
repos:
  a/one: {use: [managed], review: {ignore_authors: []}}
  a/two: {use: [managed], fix_issue: false, tend: false}
`)
	var got []string
	for _, r := range out.Rules {
		got = append(got, fmt.Sprintf("%s %s ignore=%v only=%v", r.Repo, r.Workflow, r.IgnoreAuthors, r.OnlyAuthors))
	}
	want := []string{
		"a/one review-pr ignore=[] only=[me]",
		"a/one fix-issue ignore=[] only=[]",
		"a/two review-pr ignore=[bot] only=[me]",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rules =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if len(out.Tend) != 1 || out.Tend[0].Repo != "a/one" {
		t.Errorf("tend = %+v, want a/one only", out.Tend)
	}
}

func TestUnknownProfile(t *testing.T) {
	if got := buildErr(t, "version: 1\nprofiles: {p: {review: true}}\nrepos:\n  a/b: {use: [q]}\n"); !strings.Contains(got, `unknown profile "q" (known: p)`) {
		t.Fatalf("error = %q", got)
	}
}

func TestWatchShapes(t *testing.T) {
	out := build(t, `version: 1
repos:
  a/b:
    review:
      - true
      - {on: [command], only_authors: [me]}
    fix_issue: {label: dot}
    fix_ci: {scope: global, inputs: {n: 3, flag: true}}
`)
	var got []string
	for _, r := range out.Rules {
		got = append(got, fmt.Sprintf("%s|%s|%v|%v|%v|%v", r.Workflow, r.Scope, r.On, r.Labels, r.OnlyAuthors, r.Inputs))
	}
	want := []string{
		"review-pr||[pr-opened pr-updated checks-failed]|[]|[]|map[]",
		"review-pr||[command]|[]|[me]|map[]",
		"fix-ci|global|[checks-failed]|[]|[]|map[flag:true n:3]",
		"fix-issue||[issue-labeled]|[dot]|[]|map[]",
	}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rules =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestWatchRefusals(t *testing.T) {
	cases := []struct{ name, block, want string }{
		{"unknown event", "review: {on: [pr-merged]}", `unknown event "pr-merged"`},
		{"unknown option", "review: {ignore: [x]}", "unknown option ignore"},
		{"labeled without label", "fix_issue: {on: [issue-labeled]}", "issue-labeled needs a label"},
		{"label and on", "fix_issue: {label: dot, on: [issue-opened]}", "do not combine"},
		{"label on review", "review: {label: dot}", "unknown option label"},
		{"authors not a list", "review: {only_authors: me}", "only_authors must be a list"},
		{"inputs not a map", "review: {inputs: [a]}", "inputs must be a mapping"},
		{"first contribution not a bool", "review: {ignore_first_contribution: [a]}", "ignore_first_contribution must be true or false"},
		{"empty list", "review: []", "at least one block"},
		{"scalar", "review: yes please", "must be true, a block or a list"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := buildErr(t, "version: 1\nrepos:\n  a/b:\n    "+c.block+"\n"); !strings.Contains(got, c.want) {
				t.Fatalf("error = %q, want it to contain %q", got, c.want)
			}
		})
	}
}

func TestCheckoutAndTendValues(t *testing.T) {
	for _, c := range []struct{ block, want string }{
		{"checkout: relative/path", "not an absolute path"},
		{"checkout: [a]", "must be an absolute path"},
		{"tend: {auto_merge: maybe}", "auto_merge must be true or false"},
		{"tend: {delay: 1}", "unknown option"},
		{"provider: gemini", "must be opencode or claude"},
	} {
		if got := buildErr(t, "version: 1\nrepos:\n  a/b:\n    "+c.block+"\n"); !strings.Contains(got, c.want) {
			t.Errorf("%s: error = %q, want %q", c.block, got, c.want)
		}
	}
}

// The provider reaches only the rules whose workflow declares the input.
func TestProviderSetsTheInputOfProviderAwareRulesOnly(t *testing.T) {
	out := build(t, `version: 1
defaults: {provider: claude}
repos:
  a/claude: {review: true, reply: true}
  a/opencode: {provider: opencode, review: true}
  a/explicit: {review: {inputs: {provider: opencode}}}
`)
	got := map[string]string{}
	for _, r := range out.Rules {
		got[r.Repo+" "+r.Workflow] = r.Inputs["provider"]
	}
	want := map[string]string{
		"a/claude review-pr":   "claude",
		"a/claude reply-pr":    "",
		"a/opencode review-pr": "",
		"a/explicit review-pr": "opencode",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("provider inputs = %v, want %v", got, want)
	}
}

func TestRenderedFiles(t *testing.T) {
	files, err := Build([]byte(`version: 1
settings: {max_per_run: 4}
repos:
  a/b:
    checkout: /src/b
    tend: {auto_merge: true}
    provider: claude
    review: {ignore_authors: [bot], ignore_first_contribution: true}
  c/d: {tend: true, fix_issue: {label: dot}}
`), t.TempDir(), Settings{})
	if err != nil {
		t.Fatal(err)
	}
	wantAwf := `awf_url: http://127.0.0.1:2511
state_path: /state/awf-gh/seen.jsonl
max_per_run: 4
repos:
  - repo: a/b
    workflow: review-pr
    on: [pr-opened, pr-updated, checks-failed]
    ignore_authors: [bot]
    ignore_first_contribution: true
    inputs:
      provider: claude
  - repo: c/d
    workflow: fix-issue
    on: [issue-labeled]
    labels: [dot]
`
	if got := body(files[FileAwfGh]); got != wantAwf {
		t.Errorf("awf-gh.yaml =\n%s\nwant\n%s", got, wantAwf)
	}
	if got := body(files[FileTend]); got != "a/b true\nc/d false\n" {
		t.Errorf("tend.list = %q", got)
	}
	if got := body(files[FileCheckout]); got != "a/b=/src/b\n" {
		t.Errorf("repos.map = %q", got)
	}
	if !strings.HasPrefix(string(files[FileAwfGh]), "# Generated by bin/repos") {
		t.Error("generated file has no header")
	}
}

func body(b []byte) string {
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(line, "#") {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

// bin/configure writes the awf_url and state_path that fit local or docker mode
// into awf-gh.yaml. Regenerating must keep them unless told otherwise.
func TestSettingsKeepWhatTheGeneratedFileHad(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileAwfGh), []byte("awf_url: http://127.0.0.1:9\nstate_path: /host/seen.jsonl\nmax_per_run: 9\nrepos: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := []byte("version: 1\nrepos:\n  a/b: {review: true}\n")
	files, err := Build(src, dir, Settings{})
	if err != nil {
		t.Fatal(err)
	}
	got := body(files[FileAwfGh])
	for _, want := range []string{"awf_url: http://127.0.0.1:9", "state_path: /host/seen.jsonl", "max_per_run: 9"} {
		if !strings.Contains(got, want) {
			t.Errorf("settings lost %q:\n%s", want, got)
		}
	}
	files, err = Build(src, dir, Settings{StatePath: "/override"})
	if err != nil || !strings.Contains(string(files[FileAwfGh]), "state_path: /override") {
		t.Errorf("override ignored: %v\n%s", err, files[FileAwfGh])
	}
}

func TestTendOnlyLeavesTheWatcherFileOut(t *testing.T) {
	files, err := Build([]byte("version: 1\nrepos:\n  a/b: {tend: true}\n"), t.TempDir(), Settings{})
	if err != nil {
		t.Fatalf("a tend-only file must generate: %v", err)
	}
	if _, ok := files[FileAwfGh]; ok {
		t.Errorf("awf-gh.yaml was generated without a watch rule")
	}
	if got := string(files[FileTend]); !strings.Contains(got, "a/b false\n") {
		t.Errorf("tend.list = %q, want a/b without auto_merge", got)
	}
}

func TestWatchRuleBringsTheWatcherFileBack(t *testing.T) {
	files, err := Build([]byte("version: 1\nrepos:\n  a/b: {tend: true, review: true}\n"), t.TempDir(), Settings{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := files[FileAwfGh]; !ok {
		t.Errorf("awf-gh.yaml missing although review is enabled")
	}
}

func TestWriteTouchesOnlyChangedFiles(t *testing.T) {
	dir := t.TempDir()
	files, err := Build([]byte("version: 1\nrepos:\n  a/b: {review: true}\n"), dir, Settings{})
	if err != nil {
		t.Fatal(err)
	}
	changed, err := Write(dir, files)
	if err != nil || len(changed) != 3 {
		t.Fatalf("first write = %v, %v, want all three files", changed, err)
	}
	if again, _ := Write(dir, files); len(again) != 0 {
		t.Errorf("second write changed %v", again)
	}
	if stale := Diff(dir, files); len(stale) != 0 {
		t.Errorf("diff = %v", stale)
	}
	if err := os.WriteFile(filepath.Join(dir, FileTend), []byte("hand edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if stale := Diff(dir, files); !reflect.DeepEqual(stale, []string{FileTend}) {
		t.Errorf("diff after a hand edit = %v", stale)
	}
}

// echo is a plugin: a feature written outside the built-ins, registered the way
// a new one would be. It proves the registry is the whole extension surface.
type echo struct{ base }

func (echo) Key() string { return "echo" }
func (echo) Doc() string { return "test feature: writes one line per repository to echo.txt" }
func (echo) Check(v any) error {
	if _, ok := v.(string); !ok {
		return fmt.Errorf("must be a string")
	}
	return nil
}
func (echo) Emit(r *Resolved, out *Output) error {
	out.Files["echo.txt"] = append(out.Files["echo.txt"], r.Name+": "+r.Values["echo"].(string))
	return nil
}

func TestAFeatureCanBeAddedWithoutTouchingTheLoader(t *testing.T) {
	Register(echo{})
	defer func() {
		delete(registry, "echo")
		order = order[:len(order)-1]
	}()
	files, err := Build([]byte("version: 1\ndefaults: {echo: hi}\nrepos:\n  a/b: {review: true}\n  c/d: {echo: yo}\n"), t.TempDir(), Settings{})
	if err != nil {
		t.Fatal(err)
	}
	if got := body(files["echo.txt"]); got != "a/b: hi\nc/d: yo\n" {
		t.Fatalf("echo.txt = %q", got)
	}
	if got := buildErr(t, "version: 1\nrepos:\n  a/b: {echo: [x]}\n"); !strings.Contains(got, "a/b: echo: must be a string") {
		t.Errorf("a plugin's own check was not run: %q", got)
	}
	if docs := strings.Join(FeatureDocs(), "\n"); !strings.Contains(docs, "echo") {
		t.Errorf("features listing lacks the plugin:\n%s", docs)
	}
}

func TestRegisteringTwiceIsAProgrammingError(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("a duplicate key must panic")
		}
	}()
	Register(checkout{})
}

// --- import ---------------------------------------------------------------

type ruleKey struct {
	Repo, Workflow, Scope, On, Ignore, Only, Labels, Inputs string
}

func ruleKeys(t *testing.T, data []byte) []ruleKey {
	t.Helper()
	var f awfGhFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		t.Fatal(err)
	}
	var out []ruleKey
	for _, r := range f.Repos {
		scope := r.Scope
		if scope == "" {
			scope = "local"
		}
		in := make([]string, 0, len(r.Inputs))
		for k, v := range r.Inputs {
			in = append(in, k+"="+v)
		}
		sort.Strings(in)
		out = append(out, ruleKey{r.Repo, r.Workflow, scope, fmt.Sprint(r.On), fmt.Sprint(r.IgnoreAuthors), fmt.Sprint(r.OnlyAuthors), fmt.Sprint(r.Labels), strings.Join(in, ",")})
	}
	sort.Slice(out, func(i, j int) bool { return fmt.Sprint(out[i]) < fmt.Sprint(out[j]) })
	return out
}

func lines(data []byte) []string {
	var out []string
	for _, l := range strings.Split(string(data), "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			out = append(out, l)
		}
	}
	sort.Strings(out)
	return out
}

// Importing the three hand written files and generating them again must give
// back the same rules, tend lines and checkouts, whatever the shapes.
func TestImportThenGenerateKeepsEveryRule(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{FileAwfGh, FileTend, FileCheckout, FileSync} {
		data, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	imported, err := Import(dir)
	if err != nil {
		t.Fatal(err)
	}
	files, err := Build(imported, dir, Settings{})
	if err != nil {
		t.Fatalf("imported file does not generate: %v\n%s", err, imported)
	}
	original, _ := os.ReadFile(filepath.Join(dir, FileAwfGh))
	if got, want := ruleKeys(t, files[FileAwfGh]), ruleKeys(t, original); !reflect.DeepEqual(got, want) {
		t.Fatalf("rules differ after the round trip\ngot  %v\nwant %v\n\nimported:\n%s", got, want, imported)
	}
	for name, path := range map[string]string{FileTend: FileTend, FileCheckout: FileCheckout, FileSync: FileSync} {
		orig, _ := os.ReadFile(filepath.Join(dir, path))
		if got, want := lines(files[name]), lines(orig); !reflect.DeepEqual(got, want) {
			t.Errorf("%s differs\ngot  %v\nwant %v", name, got, want)
		}
	}
	doc, err := Parse(imported)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Settings.MaxPerRun != 6 || doc.Settings.AwfURL != "" || doc.Settings.StatePath != "" {
		t.Errorf("imported settings = %+v, want only max_per_run (the others are mode dependent)", doc.Settings)
	}
	// Importing the regenerated files again must change nothing.
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	again, err := Import(dir)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != string(imported) {
		t.Errorf("import is not stable:\n%s\nvs\n%s", imported, again)
	}
}

func TestImportRefusesAWorkflowNoFeatureHandles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileAwfGh), []byte("repos:\n  - {repo: a/b, workflow: mystery, on: [pr-opened]}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(dir); err == nil || !strings.Contains(err.Error(), `no feature handles workflow "mystery"`) {
		t.Fatalf("error = %v", err)
	}
}

func TestImportKeepsEachReviewRuleProvider(t *testing.T) {
	dir := t.TempDir()
	gh := "repos:\n" +
		"  - {repo: a/b, workflow: review-pr, on: [pr-opened, pr-updated]}\n" +
		"  - {repo: a/b, workflow: review-pr, on: [checks-failed], inputs: {provider: claude}}\n"
	if err := os.WriteFile(filepath.Join(dir, FileAwfGh), []byte(gh), 0o644); err != nil {
		t.Fatal(err)
	}
	imported, err := Import(dir)
	if err != nil {
		t.Fatal(err)
	}
	files, err := Build(imported, dir, Settings{})
	if err != nil {
		t.Fatalf("imported file does not generate: %v\n%s", err, imported)
	}
	if got, want := ruleKeys(t, files[FileAwfGh]), ruleKeys(t, []byte(gh)); !reflect.DeepEqual(got, want) {
		t.Fatalf("rules differ after the round trip\ngot  %v\nwant %v\n\nimported:\n%s", got, want, imported)
	}
}

func TestImportedFileIsReadable(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{FileAwfGh, FileTend, FileCheckout, FileSync} {
		data, _ := os.ReadFile(filepath.Join("testdata", name))
		_ = os.WriteFile(filepath.Join(dir, name), data, 0o644)
	}
	imported, err := Import(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"acme/app:", "provider: claude", "fix_issue: {label: dot}", "checkout: /home/me/src/app", "sync: true", "sync: /home/me/src/lib", "only/tended:", "only/mapped:"} {
		if !strings.Contains(string(imported), want) {
			t.Errorf("imported file lacks %q:\n%s", want, imported)
		}
	}
}

func TestSyncNeedsAPath(t *testing.T) {
	if got := buildErr(t, "version: 1\nrepos:\n  a/b: {sync: true}\n"); !strings.Contains(got, "needs a `checkout` path") {
		t.Errorf("error = %q", got)
	}
	files, err := Build([]byte("version: 1\nrepos:\n  a/b: {review: true, checkout: /s/b, sync: true}\n  c/d: {sync: /s/d}\n"), t.TempDir(), Settings{})
	if err != nil {
		t.Fatal(err)
	}
	if got := body(files[FileSync]); got != "a/b=/s/b\nc/d=/s/d\n" {
		t.Errorf("sync.map = %q", got)
	}
}

// repos.example.yaml is the documentation people copy: it must keep generating.
func TestExampleFileGenerates(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "repos.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	files, err := Build(data, t.TempDir(), Settings{})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(ruleKeys(t, files[FileAwfGh])); got < 6 {
		t.Errorf("the example generates %d rules, it should show every shape", got)
	}
}

const executorDoc = "version: 1\nsettings:\n  executors:\n    pc: http://127.0.0.1:2512\nrepos:\n" +
	"  acme/own: {review: true}\n" +
	"  acme/remote: {review: true, reply: true, executor: pc}\n"

func TestExecutorSplitsTheWatcherFiles(t *testing.T) {
	files, err := Build([]byte(executorDoc), t.TempDir(), Settings{StatePath: "/s/seen.jsonl"})
	if err != nil {
		t.Fatal(err)
	}
	def, pc := string(files[FileAwfGh]), string(files[ExecutorFile("pc")])
	if !strings.Contains(def, "acme/own") || strings.Contains(def, "acme/remote") {
		t.Errorf("default file holds the wrong rules:\n%s", def)
	}
	for _, want := range []string{"acme/remote", "awf_url: http://127.0.0.1:2512", "state_path: /s/seen-pc.jsonl"} {
		if !strings.Contains(pc, want) {
			t.Errorf("executor file lost %q:\n%s", want, pc)
		}
	}
	if strings.Contains(pc, "acme/own") || strings.Count(pc, "repo: acme/remote") != 2 {
		t.Errorf("executor file should carry both rules of acme/remote and nothing else:\n%s", pc)
	}
}

func TestEveryRepositoryOnAnExecutorLeavesTheDefaultFileOut(t *testing.T) {
	src := "version: 1\nsettings:\n  executors: {pc: 'http://127.0.0.1:2512'}\nrepos:\n  acme/remote: {review: true, executor: pc}\n"
	files, err := Build([]byte(src), t.TempDir(), Settings{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := files[FileAwfGh]; ok {
		t.Errorf("the default watcher file was generated with no rule of its own")
	}
	if _, ok := files[ExecutorFile("pc")]; !ok {
		t.Errorf("the executor file is missing")
	}
}

func TestUnknownExecutorIsRefusedWithTheKnownOnes(t *testing.T) {
	src := "version: 1\nsettings:\n  executors: {pc: 'http://127.0.0.1:2512'}\nrepos:\n  acme/x: {review: true, executor: laptop}\n"
	_, err := Build([]byte(src), t.TempDir(), Settings{})
	if err == nil || !strings.Contains(err.Error(), `"laptop"`) || !strings.Contains(err.Error(), "pc") {
		t.Fatalf("error = %v, want the unknown name and the known ones", err)
	}
}

func TestExecutorSettingsAreChecked(t *testing.T) {
	for name, executors := range map[string]string{
		"a path as a name":   "'../x': http://127.0.0.1:1",
		"a dot in the name":  "'a.b': http://127.0.0.1:1",
		"an upper-case name": "PC: http://127.0.0.1:1",
		"no scheme":          "pc: 127.0.0.1:1",
		"a file address":     "pc: file:///tmp/x",
	} {
		src := "version: 1\nsettings:\n  executors: {" + executors + "}\nrepos:\n  acme/x: {review: true}\n"
		if _, err := Build([]byte(src), t.TempDir(), Settings{}); err == nil || !strings.Contains(err.Error(), "settings.executors") {
			t.Errorf("%s: error = %v, want it refused", name, err)
		}
	}
}

func TestExecutorOnATendOnlyRepositoryChangesNothing(t *testing.T) {
	src := "version: 1\nsettings:\n  executors: {pc: 'http://127.0.0.1:2512'}\nrepos:\n  acme/x: {tend: true, executor: pc}\n"
	files, err := Build([]byte(src), t.TempDir(), Settings{})
	if err != nil {
		t.Fatal(err)
	}
	for name := range files {
		if IsWatcherFile(name) {
			t.Errorf("%s generated without a watch rule", name)
		}
	}
}

func TestWatcherFileNames(t *testing.T) {
	for name, want := range map[string]bool{
		"awf-gh.yaml": true, "awf-gh.pc.yaml": true, "awf-gh.my-pc.yaml": true,
		"awf-gh.local.example.yaml": false, "awf-gh.yaml.bak": false, "tend.list": false, "awf-gh..yaml": false,
	} {
		if got := IsWatcherFile(name); got != want {
			t.Errorf("IsWatcherFile(%q) = %v, want %v", name, got, want)
		}
	}
}

const trustSettings = `settings:
  trust:
    host: %s
    allow:
      providers: [opencode]
      models: [mistral/small-latest]
      executors: [server]
  executors:
    server: http://127.0.0.1:2512
`

func trustDoc(host, block string) string {
	return "version: 1\n" + fmt.Sprintf(trustSettings, host) + "repos:\n  acme/app:\n    " + block + "\n"
}

func TestTrustRefusesWhatTheLevelForbids(t *testing.T) {
	allowed := "trust: client\n    executor: server\n    review: {inputs: {review_model: mistral/small-latest}}"
	cases := []struct{ name, host, block, want string }{
		{"an allowed client block", "client", allowed, ""},
		{"auto_merge", "client", allowed + "\n    tend: {auto_merge: true}", "auto_merge is refused on a client repository"},
		{"no executor", "client", "trust: client", "must name an executor"},
		{"an unlisted executor", "client", "trust: client\n    executor: laptop", "executor \"laptop\" is not in settings.trust.allow.executors"},
		{"an unlisted model", "client", allowed + "\n    reply: {inputs: {model: mistral/big-latest}}", "model \"mistral/big-latest\" is not in settings.trust.allow.models"},
		{"a provider that is not allowed", "client", "trust: client\n    provider: claude\n    executor: server", "provider \"claude\" is not in settings.trust.allow.providers"},
		{"a client repository on an open host", "open", allowed, "settings.trust.host is open"},
		{"a strict repository on a client host", "client", "trust: strict\n    executor: server", "settings.trust.host is client"},
		{"a checkout on a strict repository", "strict", "trust: strict\n    executor: server\n    checkout: /tmp/acme", "keeps no local checkout"},
		{"open takes anything", "open", "trust: open\n    tend: {auto_merge: true}\n    checkout: /tmp/acme\n    review: {inputs: {review_model: anything/at-all}}", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			doc, err := Parse([]byte(trustDoc(c.host, c.block)))
			if err != nil {
				t.Fatal(err)
			}
			_, err = Generate(doc)
			switch {
			case c.want == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
				t.Fatalf("error = %v, want it to contain %q", err, c.want)
			}
		})
	}
}

func TestTrustRefusesAnUnknownLevelAndABadSetting(t *testing.T) {
	if got := buildErr(t, "version: 1\nrepos:\n  a/b:\n    trust: secret\n"); !strings.Contains(got, "must be one of open, client, strict") {
		t.Fatalf("error = %q", got)
	}
	for _, c := range []struct{ name, settings, want string }{
		{"a host nobody knows", "trust: {host: banana}", `settings.trust.host: "banana"`},
		{"an empty entry", "trust: {allow: {models: ['']}}", "settings.trust.allow.models"},
	} {
		t.Run(c.name, func(t *testing.T) {
			src := "version: 1\nsettings:\n  " + c.settings + "\nrepos:\n  a/b:\n    tend: true\n"
			doc, err := Parse([]byte(src))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := BuildDocument(doc, t.TempDir(), Settings{}); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error = %v, want it to contain %q", err, c.want)
			}
		})
	}
}
