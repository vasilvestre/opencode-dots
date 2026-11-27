package models

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
)

var presetWorkflowFiles = []string{"_fix-issue-external.yaml", "_fix-issue-owned.yaml", "ask-pr.yaml", "fix-ci.yaml", "reply-pr.yaml", "review-pr.yaml", "tend-repo.yaml"}

func testModelValues() map[string]string {
	return map[string]string{
		"ask-pr/model":                   "provider/light",
		"fix-ci/model":                   "provider/light",
		"_fix-issue-owned/model":         "provider/default",
		"_fix-issue-external/model":      "provider/default",
		"reply-pr/model":                 "provider/light",
		"review-pr/review_model":         "provider/review",
		"review-pr/address_model":        "provider/address",
		"review-pr/claude_review_model":  "sonnet",
		"review-pr/claude_address_model": "haiku",
		"tend-repo/model":                "provider/default",
	}
}

func testMatchingValues() map[string]string {
	return map[string]string{
		"ask-pr/model":                   "provider/light",
		"fix-ci/model":                   "provider/light",
		"_fix-issue-owned/model":         "provider/default",
		"_fix-issue-external/model":      "provider/default",
		"reply-pr/model":                 "provider/light",
		"review-pr/review_model":         "provider/default",
		"review-pr/address_model":        "provider/light",
		"review-pr/claude_review_model":  "sonnet",
		"review-pr/claude_address_model": "haiku",
		"tend-repo/model":                "provider/default",
	}
}

func writePresetWorkflows(t *testing.T, root string, values map[string]string) {
	t.Helper()
	dir := filepath.Join(root, "workflows")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range presetWorkflowFiles {
		workflow := strings.TrimSuffix(name, ".yaml")
		var data strings.Builder
		fmt.Fprintf(&data, "# keep %s header\nname: %s\nversion: \"1\"\ninputs:\n", workflow, workflow)
		for _, role := range roles {
			if role.workflow != workflow {
				continue
			}
			fmt.Fprintf(&data, "  - name: %s\n    type: string\n    required: false\n    description: %s model\n    default: %s # keep %s default\n    validation:\n      pattern: '%s'\n", role.input, role.input, values[role.key()], role.key(), role.pattern())
		}
		if workflow == "review-pr" {
			data.WriteString("  - name: provider\n    type: string\n    required: false\n    default: opencode\n")
		}
		data.WriteString("states:\n  initial: ")
		for _, role := range roles {
			if role.workflow == workflow && role.state != "" {
				if role.pick != "" {
					data.WriteString(role.pick + "\n")
				} else {
					data.WriteString(role.state + "\n")
				}
				break
			}
		}
		for _, role := range roles {
			if role.workflow != workflow || role.state == "" {
				continue
			}
			model := "{{.inputs." + role.input + "}}"
			next := "done"
			if role.pick != "" {
				model = "{{.states." + role.pick + ".Output}}"
				next = role.state
				fmt.Fprintf(&data, "  %s:\n    type: step\n    command: |\n      echo '{{.inputs.provider}} {{.inputs.%s}} {{.inputs.%s}}'\n    on_success: %s\n    on_failure: error\n", role.pick, role.input, role.claudeInput, role.state)
			}
			_ = next
			fmt.Fprintf(&data, "  %s:\n    type: agent\n    provider: opencode\n    options:\n      model: \"%s\"\n    prompt: |\n      Keep this prompt for %s.\n      Do not rewrite this line.\n    on_success: done\n    on_failure: error\n", role.state, model, workflow)
		}
		data.WriteString("  done:\n    type: terminal\n  error:\n    type: terminal\n")
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func presetResolver(root string) func(string) (string, bool) {
	return func(rel string) (string, bool) {
		if !strings.HasPrefix(rel, "workflows/") || filepath.Clean(rel) != rel {
			return "", false
		}
		return filepath.Join(root, rel), true
	}
}

func readPresetSnapshot(t *testing.T, root string) *Snapshot {
	t.Helper()
	snapshot, err := ReadConfigured(presetWorkflowFiles, presetResolver(root), filepath.Join(root, presetsFile))
	if err != nil {
		t.Fatalf("ReadConfigured: %v", err)
	}
	return snapshot
}

func writePresetConfig(t *testing.T, root, data string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, presetsFile), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func presetConfig(light, fallback, overrides string) string {
	data := "version: 1\npresets:\n  light: " + light + "\n  default: " + fallback + "\n"
	if overrides != "" {
		data += "overrides:\n" + overrides
	}
	return data
}

func pointer(value string) *string { return &value }

func assertStringMap(t *testing.T, got, want map[string]string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("map = %#v, want %#v", got, want)
	}
}

func assertPlanPaths(t *testing.T, plan *Plan, want []string) {
	t.Helper()
	got := make([]string, 0, len(plan.changes))
	for _, change := range plan.changes {
		got = append(got, change.path)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("plan paths = %q, want %q", got, want)
	}
}

func TestPresetMigrationPreservesRuntimeValues(t *testing.T) {
	root := t.TempDir()
	values := testModelValues()
	writePresetWorkflows(t, root, values)
	snapshot := readPresetSnapshot(t, root)

	if !snapshot.MigrationRequired() {
		t.Fatal("missing preset file must require migration")
	}
	if _, err := os.Stat(filepath.Join(root, presetsFile)); !os.IsNotExist(err) {
		t.Fatalf("GET created the preset file: %v", err)
	}
	assertStringMap(t, snapshot.Presets(), map[string]string{"light": "provider/light", "default": "provider/default", "claude_light": "haiku", "claude_default": "sonnet"})
	wantOverrides := map[string]string{
		"review-pr/review_model":  "provider/review",
		"review-pr/address_model": "provider/address",
	}
	gotOverrides := map[string]string{}
	for _, variable := range snapshot.Variables() {
		if variable.Value != values[variable.Key] {
			t.Fatalf("%s changed during migration: %q", variable.Key, variable.Value)
		}
		if variable.Override != nil {
			gotOverrides[variable.Key] = *variable.Override
		}
	}
	assertStringMap(t, gotOverrides, wantOverrides)
}

func TestPresetRoleMappingAndJSONFields(t *testing.T) {
	root := t.TempDir()
	writePresetWorkflows(t, root, testMatchingValues())
	writePresetConfig(t, root, presetConfig("provider/light", "provider/default", ""))
	snapshot := readPresetSnapshot(t, root)

	wantPresets := map[string]string{
		"ask-pr/model":                   "light",
		"fix-ci/model":                   "light",
		"_fix-issue-owned/model":         "default",
		"_fix-issue-external/model":      "default",
		"reply-pr/model":                 "light",
		"review-pr/review_model":         "default",
		"review-pr/address_model":        "light",
		"review-pr/claude_review_model":  "claude_default",
		"review-pr/claude_address_model": "claude_light",
		"tend-repo/model":                "default",
	}
	for _, variable := range snapshot.Variables() {
		if variable.Preset != wantPresets[variable.Key] {
			t.Errorf("%s preset = %q, want %q", variable.Key, variable.Preset, wantPresets[variable.Key])
		}
		if variable.Override != nil {
			t.Errorf("%s has an unexpected override", variable.Key)
		}
		data, err := json.Marshal(variable)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]any
		if err := json.Unmarshal(data, &fields); err != nil {
			t.Fatal(err)
		}
		if fields["preset"] != variable.Preset {
			t.Errorf("%s JSON preset = %#v", variable.Key, fields["preset"])
		}
		if value, ok := fields["override"]; !ok || value != nil {
			t.Errorf("%s JSON override = %#v, want null", variable.Key, value)
		}
	}
}

func TestPresetUpdatePropagatesAndWritesWorkflowsFirst(t *testing.T) {
	root := t.TempDir()
	writePresetWorkflows(t, root, testModelValues())
	writePresetConfig(t, root, presetConfig("provider/light", "provider/default", "  review-pr/review_model: provider/review\n  review-pr/address_model: provider/address\n"))
	snapshot := readPresetSnapshot(t, root)
	plan, err := snapshot.PrepareUpdate(Update{Presets: map[string]string{"light": "provider/new-light", "default": "provider/new-default"}})
	if err != nil {
		t.Fatal(err)
	}
	assertPlanPaths(t, plan, []string{
		"workflows/_fix-issue-external.yaml",
		"workflows/_fix-issue-owned.yaml",
		"workflows/ask-pr.yaml",
		"workflows/fix-ci.yaml",
		"workflows/reply-pr.yaml",
		"workflows/tend-repo.yaml",
		presetsFile,
	})

	var writes []string
	backup := func(abs string) (string, error) {
		data, err := os.ReadFile(abs)
		if err != nil {
			return "", err
		}
		path := abs + ".bak"
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return "", err
		}
		return filepath.Base(path), nil
	}
	write := func(abs string, data []byte) error {
		writes = append(writes, abs)
		return os.WriteFile(abs, data, 0o644)
	}
	saved, err := plan.Apply(backup, write)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved) != 7 {
		t.Fatalf("saved %d files, want 7", len(saved))
	}
	for i, change := range saved {
		if change.Backup == "" {
			t.Errorf("%s has no backup", change.Path)
		}
		if i < len(saved)-1 && writes[i] != filepath.Join(root, change.Path) {
			t.Errorf("write %d = %q, want %q", i, writes[i], filepath.Join(root, change.Path))
		}
	}
	if writes[len(writes)-1] != filepath.Join(root, presetsFile) {
		t.Fatalf("preset file was not written last: %q", writes)
	}

	after := readPresetSnapshot(t, root)
	want := map[string]string{
		"ask-pr/model":                   "provider/new-light",
		"fix-ci/model":                   "provider/new-light",
		"_fix-issue-owned/model":         "provider/new-default",
		"_fix-issue-external/model":      "provider/new-default",
		"reply-pr/model":                 "provider/new-light",
		"review-pr/review_model":         "provider/review",
		"review-pr/address_model":        "provider/address",
		"review-pr/claude_review_model":  "sonnet",
		"review-pr/claude_address_model": "haiku",
		"tend-repo/model":                "provider/new-default",
	}
	for _, variable := range after.Variables() {
		if variable.Value != want[variable.Key] {
			t.Errorf("%s = %q, want %q", variable.Key, variable.Value, want[variable.Key])
		}
	}

	idempotent, err := after.PrepareUpdate(Update{Presets: map[string]string{"light": "provider/new-light", "default": "provider/new-default"}})
	if err != nil {
		t.Fatal(err)
	}
	assertPlanPaths(t, idempotent, nil)
	unchanged, err := idempotent.Apply(func(string) (string, error) {
		t.Fatal("unchanged save made a backup")
		return "", nil
	}, func(string, []byte) error {
		t.Fatal("unchanged save wrote a file")
		return nil
	})
	if err != nil || len(unchanged) != 0 {
		t.Fatalf("unchanged save = %#v, %v", unchanged, err)
	}
}

func TestPresetFirstSaveCreatesFileWithoutBackup(t *testing.T) {
	root := t.TempDir()
	writePresetWorkflows(t, root, testModelValues())
	snapshot := readPresetSnapshot(t, root)
	plan, err := snapshot.PrepareUpdate(Update{Presets: map[string]string{"light": "provider/light", "default": "provider/default"}})
	if err != nil {
		t.Fatal(err)
	}
	assertPlanPaths(t, plan, []string{presetsFile})
	if !plan.changes[0].created {
		t.Fatal("first preset save must create the file")
	}
	saved, err := plan.Apply(func(string) (string, error) {
		t.Fatal("first preset save made a backup")
		return "", nil
	}, func(abs string, data []byte) error {
		return os.WriteFile(abs, data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(saved) != 1 || saved[0].Path != presetsFile || saved[0].Backup != "" {
		t.Fatalf("saved = %#v", saved)
	}
	configuration, err := parseConfiguration(mustReadFile(t, filepath.Join(root, presetsFile)))
	if err != nil {
		t.Fatal(err)
	}
	assertStringMap(t, configuration.Presets, snapshot.Presets())
	if len(configuration.Overrides) != 2 {
		t.Fatalf("created overrides = %#v, want the two migration overrides", configuration.Overrides)
	}
}

func TestPresetOverrideClearingPreservesMissingKeys(t *testing.T) {
	root := t.TempDir()
	values := testMatchingValues()
	values["ask-pr/model"] = "provider/custom"
	values["review-pr/review_model"] = "provider/reviewer"
	writePresetWorkflows(t, root, values)
	writePresetConfig(t, root, presetConfig("provider/light", "provider/default", "  ask-pr/model: provider/custom\n  review-pr/review_model: provider/reviewer\n"))
	snapshot := readPresetSnapshot(t, root)
	plan, err := snapshot.PrepareUpdate(Update{
		Presets:   map[string]string{"light": "provider/light", "default": "provider/default"},
		Overrides: map[string]*string{"ask-pr/model": nil},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertPlanPaths(t, plan, []string{"workflows/ask-pr.yaml", presetsFile})
	if _, err := plan.Apply(presetBackup, presetWrite); err != nil {
		t.Fatal(err)
	}
	configuration, err := parseConfiguration(mustReadFile(t, filepath.Join(root, presetsFile)))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := configuration.Overrides["ask-pr/model"]; ok {
		t.Fatal("cleared override is still present")
	}
	if value := configuration.Overrides["review-pr/review_model"]; value == nil || *value != "provider/reviewer" {
		t.Fatalf("missing override key was not preserved: %#v", configuration.Overrides)
	}
	after := readPresetSnapshot(t, root)
	for _, variable := range after.Variables() {
		if variable.Key == "ask-pr/model" && variable.Value != "provider/light" {
			t.Fatalf("cleared role = %q, want provider/light", variable.Value)
		}
	}
}

func TestPresetConfigRejectsInvalidShapes(t *testing.T) {
	cases := []struct {
		name string
		data string
		want string
	}{
		{"empty", "", "empty"},
		{"invalid YAML", "version: [\n", "yaml"},
		{"multiple documents", presetConfig("provider/light", "provider/default", "") + "---\nversion: 1\n", "one YAML document"},
		{"unknown root key", presetConfig("provider/light", "provider/default", "") + "extra: true\n", "field extra not found"},
		{"duplicate root key", "version: 1\nversion: 1\npresets: {light: provider/light, default: provider/default}\n", "mapping key \"version\" already defined"},
		{"unsupported version", strings.Replace(presetConfig("provider/light", "provider/default", ""), "version: 1", "version: 2", 1), "unsupported preset version 2"},
		{"string version", strings.Replace(presetConfig("provider/light", "provider/default", ""), "version: 1", "version: \"1\"", 1), "cannot unmarshal"},
		{"missing preset", "version: 1\npresets: {light: provider/light}\n", "missing: default"},
		{"unknown preset", "version: 1\npresets: {light: provider/light, default: provider/default, other: provider/other}\n", "unknown: other"},
		{"duplicate preset", "version: 1\npresets: {light: provider/light, light: provider/other, default: provider/default}\n", "already defined"},
		{"empty preset", presetConfig("\"\"", "provider/default", ""), "preset \"light\""},
		{"invalid preset", presetConfig("not-a-model", "provider/default", ""), "preset \"light\""},
		{"long preset", presetConfig(strings.Repeat("a", 201)+"/model", "provider/default", ""), "longer than 200"},
		{"unknown role", presetConfig("provider/light", "provider/default", "  other/model: provider/other\n"), "unknown model role"},
		{"unknown null role", presetConfig("provider/light", "provider/default", "  other/model: null\n"), "unknown model role"},
		{"duplicate override", presetConfig("provider/light", "provider/default", "  ask-pr/model: provider/one\n  ask-pr/model: provider/two\n"), "already defined"},
		{"invalid override", presetConfig("provider/light", "provider/default", "  ask-pr/model: nope\n"), "override \"ask-pr/model\""},
		{"equal effective models", presetConfig("provider/same", "provider/same", ""), "must be different"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			writePresetWorkflows(t, root, testMatchingValues())
			writePresetConfig(t, root, c.data)
			if _, err := ReadConfigured(presetWorkflowFiles, presetResolver(root), filepath.Join(root, presetsFile)); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error = %v, want substring %q", err, c.want)
			}
		})
	}
}

func TestPresetUpdateRejectsInvalidValues(t *testing.T) {
	cases := []struct {
		name   string
		update Update
		want   string
	}{
		{"missing preset", Update{Presets: map[string]string{"light": "provider/light"}}, "missing: default"},
		{"unknown preset", Update{Presets: map[string]string{"light": "provider/light", "default": "provider/default", "other": "provider/other"}}, "unknown: other"},
		{"invalid preset", Update{Presets: map[string]string{"light": "nope", "default": "provider/default"}}, "preset \"light\""},
		{"long preset", Update{Presets: map[string]string{"light": strings.Repeat("a", 201) + "/model", "default": "provider/default"}}, "longer than 200"},
		{"unknown override", Update{Presets: map[string]string{"light": "provider/light", "default": "provider/default"}, Overrides: map[string]*string{"other/model": pointer("provider/other")}}, "unknown model role"},
		{"invalid override", Update{Presets: map[string]string{"light": "provider/light", "default": "provider/default"}, Overrides: map[string]*string{"ask-pr/model": pointer("nope")}}, "override \"ask-pr/model\""},
		{"equal effective models", Update{Presets: map[string]string{"light": "provider/same", "default": "provider/same"}}, "must be different"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			writePresetWorkflows(t, root, testMatchingValues())
			writePresetConfig(t, root, presetConfig("provider/light", "provider/default", ""))
			snapshot := readPresetSnapshot(t, root)
			if plan, err := snapshot.PrepareUpdate(c.update); err == nil || plan != nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("plan = %#v, error = %v, want %q", plan, err, c.want)
			}
		})
	}
}

func TestPresetDriftBlocksPrepare(t *testing.T) {
	root := t.TempDir()
	values := testMatchingValues()
	values["ask-pr/model"] = "provider/manual"
	writePresetWorkflows(t, root, values)
	writePresetConfig(t, root, presetConfig("provider/light", "provider/default", ""))
	before := mustReadFile(t, filepath.Join(root, presetsFile))
	snapshot := readPresetSnapshot(t, root)
	for _, variable := range snapshot.Variables() {
		if variable.Key == "ask-pr/model" && !strings.Contains(variable.Conflict, "differs from resolved model") {
			t.Fatalf("drift conflict = %q", variable.Conflict)
		}
	}
	plan, err := snapshot.PrepareUpdate(Update{Presets: map[string]string{"light": "provider/new", "default": "provider/default"}})
	if err == nil || plan != nil {
		t.Fatalf("drift plan = %#v, error = %v", plan, err)
	}
	if after := mustReadFile(t, filepath.Join(root, presetsFile)); !reflect.DeepEqual(after, before) {
		t.Fatal("drift changed the preset file")
	}
}

func TestPresetConfigRejectsEqualReviewAndAddress(t *testing.T) {
	root := t.TempDir()
	writePresetWorkflows(t, root, testMatchingValues())
	writePresetConfig(t, root, presetConfig("provider/light", "provider/default", "  review-pr/review_model: provider/light\n"))
	if _, err := ReadConfigured(presetWorkflowFiles, presetResolver(root), filepath.Join(root, presetsFile)); err == nil || !strings.Contains(err.Error(), "must be different") {
		t.Fatalf("error = %v", err)
	}
}

func TestPresetRollbackRestoresExistingFiles(t *testing.T) {
	root := t.TempDir()
	writePresetWorkflows(t, root, testMatchingValues())
	writePresetConfig(t, root, presetConfig("provider/light", "provider/default", ""))
	originals := map[string][]byte{}
	for _, name := range presetWorkflowFiles {
		originals[filepath.Join(root, "workflows", name)] = mustReadFile(t, filepath.Join(root, "workflows", name))
	}
	presetAbs := filepath.Join(root, presetsFile)
	originals[presetAbs] = mustReadFile(t, presetAbs)
	snapshot := readPresetSnapshot(t, root)
	plan, err := snapshot.PrepareUpdate(Update{Presets: map[string]string{"light": "provider/new-light", "default": "provider/new-default"}})
	if err != nil {
		t.Fatal(err)
	}
	failed := false
	write := func(abs string, data []byte) error {
		if abs == presetAbs && !failed {
			failed = true
			if err := os.WriteFile(abs, []byte("partial preset"), 0o644); err != nil {
				return err
			}
			return errors.New("injected preset write failure")
		}
		return os.WriteFile(abs, data, 0o644)
	}
	if _, err := plan.Apply(presetBackup, write); err == nil || !strings.Contains(err.Error(), "injected preset write failure") {
		t.Fatalf("Apply error = %v", err)
	}
	for path, want := range originals {
		if got := mustReadFile(t, path); !reflect.DeepEqual(got, want) {
			t.Errorf("%s was not restored", path)
		}
	}
}

func TestPresetRollbackRemovesCreatedFile(t *testing.T) {
	root := t.TempDir()
	writePresetWorkflows(t, root, testMatchingValues())
	originals := map[string][]byte{}
	for _, name := range presetWorkflowFiles {
		path := filepath.Join(root, "workflows", name)
		originals[path] = mustReadFile(t, path)
	}
	snapshot := readPresetSnapshot(t, root)
	plan, err := snapshot.PrepareUpdate(Update{Presets: map[string]string{"light": "provider/new-light", "default": "provider/new-default"}})
	if err != nil {
		t.Fatal(err)
	}
	presetAbs := filepath.Join(root, presetsFile)
	failed := false
	write := func(abs string, data []byte) error {
		if abs == presetAbs && !failed {
			failed = true
			if err := os.WriteFile(abs, data, 0o644); err != nil {
				return err
			}
			return errors.New("injected creation failure")
		}
		return os.WriteFile(abs, data, 0o644)
	}
	if _, err := plan.Apply(presetBackup, write); err == nil || !strings.Contains(err.Error(), "injected creation failure") {
		t.Fatalf("Apply error = %v", err)
	}
	if _, err := os.Stat(presetAbs); !os.IsNotExist(err) {
		t.Fatalf("new preset file was not removed: %v", err)
	}
	for path, want := range originals {
		if got := mustReadFile(t, path); !reflect.DeepEqual(got, want) {
			t.Errorf("%s was not restored", path)
		}
	}
}

func TestPresetSavePreservesWorkflowBytes(t *testing.T) {
	root := t.TempDir()
	writePresetWorkflows(t, root, testModelValues())
	askPath := filepath.Join(root, "workflows", "ask-pr.yaml")
	replyPath := filepath.Join(root, "workflows", "reply-pr.yaml")
	ask := strings.ReplaceAll(string(mustReadFile(t, askPath)), "default: provider/light # keep ask-pr/model default", "default: \"provider/light\" # keep ask-pr/model default")
	reply := strings.ReplaceAll(string(mustReadFile(t, replyPath)), "default: provider/light # keep reply-pr/model default", "default: 'provider/light' # keep reply-pr/model default")
	ask = strings.ReplaceAll(ask, "\n", "\r\n")
	reply = strings.ReplaceAll(reply, "\n", "\r\n")
	if err := os.WriteFile(askPath, []byte(ask), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(replyPath, []byte(reply), 0o644); err != nil {
		t.Fatal(err)
	}
	snapshot := readPresetSnapshot(t, root)
	plan, err := snapshot.PrepareUpdate(Update{Presets: map[string]string{"light": "provider/new#fast", "default": "provider/default"}})
	if err != nil {
		t.Fatal(err)
	}
	assertPlanPaths(t, plan, []string{"workflows/ask-pr.yaml", "workflows/fix-ci.yaml", "workflows/reply-pr.yaml", presetsFile})
	changes := map[string][]byte{}
	for _, change := range plan.changes {
		changes[change.path] = change.data
	}
	wantAsk := strings.Replace(ask, "\"provider/light\"", strconv.Quote("provider/new#fast"), 1)
	wantReply := strings.Replace(reply, "'provider/light'", "'provider/new#fast'", 1)
	if string(changes["workflows/ask-pr.yaml"]) != wantAsk {
		t.Fatal("double-quoted workflow bytes changed outside the default token")
	}
	if string(changes["workflows/reply-pr.yaml"]) != wantReply {
		t.Fatal("single-quoted workflow bytes changed outside the default token")
	}
	if _, err := plan.Apply(presetBackup, presetWrite); err != nil {
		t.Fatal(err)
	}
	if string(mustReadFile(t, askPath)) != wantAsk || string(mustReadFile(t, replyPath)) != wantReply {
		t.Fatal("written workflow bytes differ from the prepared replacements")
	}
}

func TestLegacyPrepareStillWritesOnlyWorkflows(t *testing.T) {
	root := t.TempDir()
	writePresetWorkflows(t, root, testModelValues())
	snapshot, err := Read(presetWorkflowFiles, presetResolver(root))
	if err != nil {
		t.Fatal(err)
	}
	values := testModelValues()
	values["ask-pr/model"] = "provider/custom"
	plan, err := snapshot.Prepare(values)
	if err != nil {
		t.Fatal(err)
	}
	assertPlanPaths(t, plan, []string{"workflows/ask-pr.yaml"})
	if _, err := plan.Apply(presetBackup, presetWrite); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, presetsFile)); !os.IsNotExist(err) {
		t.Fatalf("legacy prepare created a preset file: %v", err)
	}
}

func presetBackup(abs string) (string, error) {
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	path := abs + ".bak"
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}
	return filepath.Base(path), nil
}

func presetWrite(abs string, data []byte) error {
	return os.WriteFile(abs, data, 0o644)
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestPresetOverridesSerializeInRoleOrder(t *testing.T) {
	configuration := &Configuration{
		Version: presetVersion,
		Presets: map[string]string{"light": "provider/light", "default": "provider/default"},
		Overrides: map[string]*string{
			"tend-repo/model": pointer("provider/z"),
			"ask-pr/model":    pointer("provider/a"),
		},
	}
	data, err := configuration.marshal()
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Index(text, "ask-pr/model") > strings.Index(text, "tend-repo/model") {
		t.Fatalf("overrides are not sorted:\n%s", text)
	}
	if !strings.HasSuffix(text, "\n") {
		t.Fatal("serialized preset file has no final newline")
	}
	keys := make([]string, 0, len(configuration.Overrides))
	for key := range configuration.Overrides {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	if !reflect.DeepEqual(keys, []string{"ask-pr/model", "tend-repo/model"}) {
		t.Fatalf("unexpected fixture keys: %q", keys)
	}
}

func TestClaudePresetsUseTheirOwnPattern(t *testing.T) {
	root := t.TempDir()
	writePresetWorkflows(t, root, testMatchingValues())
	writePresetConfig(t, root, presetConfig("provider/light", "provider/default", ""))
	snapshot := readPresetSnapshot(t, root)
	base := map[string]string{"light": "provider/light", "default": "provider/default"}
	with := func(key, value string) map[string]string {
		presets := cloneStrings(base)
		presets[key] = value
		return presets
	}
	cases := []struct {
		name    string
		presets map[string]string
		want    string
	}{
		{"opencode id in a claude preset", with("claude_light", "claude-agent/sonnet"), `preset "claude_light"`},
		{"claude alias in an opencode preset", with("light", "sonnet"), `preset "light"`},
		{"equal claude pair", with("claude_default", "haiku"), "claude_review_model and claude_address_model must be different"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			plan, err := snapshot.PrepareUpdate(Update{Presets: c.presets})
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("plan = %v, error = %v, want %q", plan, err, c.want)
			}
		})
	}
	for _, id := range []string{"haiku", "claude-haiku-4-5", "claude-sonnet-4-5", "sonnet[1m]"} {
		if _, err := snapshot.PrepareUpdate(Update{Presets: with("claude_light", id)}); err != nil {
			t.Errorf("claude model %q refused: %v", id, err)
		}
	}
}

func TestOldPresetFileGetsClaudeDefaults(t *testing.T) {
	root := t.TempDir()
	writePresetWorkflows(t, root, testMatchingValues())
	writePresetConfig(t, root, presetConfig("provider/light", "provider/default", ""))
	snapshot := readPresetSnapshot(t, root)
	if snapshot.MigrationRequired() {
		t.Fatal("a valid file without claude presets must not need migration")
	}
	presets := snapshot.Presets()
	if presets["claude_light"] != "haiku" || presets["claude_default"] != "sonnet" {
		t.Fatalf("presets = %v, want claude_light haiku and claude_default sonnet", presets)
	}
	for _, variable := range snapshot.Variables() {
		if variable.Conflict != "" {
			t.Errorf("%s: %s", variable.Key, variable.Conflict)
		}
		wantProvider := "opencode"
		if strings.Contains(variable.Input, "claude_") {
			wantProvider = "claude"
		}
		if variable.Provider != wantProvider {
			t.Errorf("%s provider = %q, want %q", variable.Key, variable.Provider, wantProvider)
		}
	}
}

func TestClaudePresetSaveRoundTrips(t *testing.T) {
	root := t.TempDir()
	writePresetWorkflows(t, root, testMatchingValues())
	writePresetConfig(t, root, presetConfig("provider/light", "provider/default", ""))
	snapshot := readPresetSnapshot(t, root)
	plan, err := snapshot.PrepareUpdate(Update{
		Presets: map[string]string{"light": "provider/light", "default": "provider/default", "claude_light": "claude-haiku-4-5", "claude_default": "sonnet[1m]"},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertPlanPaths(t, plan, []string{"workflows/review-pr.yaml", presetsFile})
	if _, err := plan.Apply(func(abs string) (string, error) { return abs + ".bak", nil }, func(abs string, data []byte) error {
		return os.WriteFile(abs, data, 0o644)
	}); err != nil {
		t.Fatal(err)
	}
	after := readPresetSnapshot(t, root)
	got := map[string]string{}
	for _, variable := range after.Variables() {
		got[variable.Key] = variable.Value
		if variable.Conflict != "" {
			t.Errorf("%s: %s", variable.Key, variable.Conflict)
		}
	}
	if got["review-pr/claude_review_model"] != "sonnet[1m]" || got["review-pr/claude_address_model"] != "claude-haiku-4-5" {
		t.Fatalf("claude defaults = %q and %q", got["review-pr/claude_review_model"], got["review-pr/claude_address_model"])
	}
	if got["review-pr/review_model"] != "provider/default" {
		t.Fatalf("opencode default changed to %q", got["review-pr/review_model"])
	}
}
