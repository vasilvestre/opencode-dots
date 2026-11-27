package main

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"opencode-dots/internal/models"
)

type testModelInput struct {
	name  string
	state string
	// pick is the step the agent reads its model from; claude marks the
	// input as a claude model with no agent step of its own.
	pick   string
	claude bool
}

func testModelDefaults() map[string]string {
	return map[string]string{
		"ask-pr/model":                   "provider/ask",
		"fix-ci/model":                   "provider/fix-ci",
		"_fix-issue-owned/model":         "provider/fix-issue",
		"_fix-issue-external/model":      "provider/fix-issue",
		"reply-pr/model":                 "provider/reply",
		"review-pr/review_model":         "provider/review",
		"review-pr/address_model":        "provider/address",
		"review-pr/claude_review_model":  "sonnet",
		"review-pr/claude_address_model": "haiku",
		"tend-repo/model":                "provider/learn",
	}
}

func describe(input testModelInput) string {
	if input.state != "" {
		return input.state
	}
	return input.name
}

func writeModelWorkflows(t *testing.T, root string, overrides map[string]string) map[string]string {
	t.Helper()
	defaults := testModelDefaults()
	for key, value := range overrides {
		if _, ok := defaults[key]; !ok {
			t.Fatalf("unknown fixture model key %q", key)
		}
		defaults[key] = value
	}
	groups := []struct {
		file   string
		inputs []testModelInput
	}{
		{"ask-pr.yaml", []testModelInput{{name: "model", state: "ask"}}},
		{"fix-ci.yaml", []testModelInput{{name: "model", state: "fix"}}},
		{"_fix-issue-owned.yaml", []testModelInput{{name: "model", state: "fix"}}},
		{"_fix-issue-external.yaml", []testModelInput{{name: "model", state: "fix"}}},
		{"reply-pr.yaml", []testModelInput{{name: "model", state: "reply"}}},
		{"review-pr.yaml", []testModelInput{
			{name: "review_model", state: "review", pick: "pick_review_model"},
			{name: "address_model", state: "address", pick: "pick_address_model"},
			{name: "claude_review_model", claude: true},
			{name: "claude_address_model", claude: true},
		}},
		{"tend-repo.yaml", []testModelInput{{name: "model", state: "learn"}}},
	}
	originals := make(map[string]string, len(groups))
	for _, group := range groups {
		workflow := strings.TrimSuffix(group.file, ".yaml")
		var body strings.Builder
		body.WriteString("name: " + workflow + "\nversion: \"1.0.0\"\n\n# Keep the section comment.\ninputs:\n")
		body.WriteString("  - name: token\n    type: string\n    required: true\n")
		if workflow == "review-pr" {
			body.WriteString("  - name: provider\n    type: string\n    required: false\n    default: opencode\n")
		}
		for _, input := range group.inputs {
			pattern := models.Pattern
			if input.claude {
				pattern = models.ClaudePattern
			}
			body.WriteString("  - name: " + input.name + "\n" +
				"    type: string\n" +
				"    required: false\n" +
				"    default: " + defaults[workflow+"/"+input.name] + " # keep this comment\n" +
				"    description: model for the " + describe(input) + " agent\n" +
				"    validation:\n" +
				"      pattern: '" + pattern + "'\n")
		}
		initial := group.inputs[0].state
		if group.inputs[0].pick != "" {
			initial = group.inputs[0].pick
		}
		body.WriteString("\nstates:\n  initial: " + initial + "\n")
		for _, input := range group.inputs {
			if input.claude {
				continue
			}
			model := "{{.inputs." + input.name + "}}"
			if input.pick != "" {
				model = "{{.states." + input.pick + ".Output}}"
				body.WriteString("  " + input.pick + ":\n" +
					"    type: step\n" +
					"    command: echo '{{.inputs.provider}} {{.inputs." + input.name + "}} {{.inputs.claude_" + input.name + "}}'\n" +
					"    on_success: " + input.state + "\n" +
					"    on_failure: error\n")
			}
			body.WriteString("  " + input.state + ":\n" +
				"    type: agent\n" +
				"    provider: opencode\n" +
				"    options:\n" +
				"      model: \"" + model + "\"\n" +
				"    prompt: |\n" +
				"      Keep this prompt and {{.inputs." + input.name + "}} reference unchanged.\n" +
				"    on_success: done\n" +
				"    on_failure: error\n")
		}
		body.WriteString("  done:\n    type: terminal\n    status: success\n" +
			"  error:\n    type: terminal\n    status: failure\n")
		path := filepath.Join(root, "workflows", group.file)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		originals[group.file] = body.String()
	}
	return originals
}

func currentModelValues(t *testing.T, d *dash) map[string]string {
	t.Helper()
	values := map[string]string{}
	for _, variable := range getModelVariables(t, d) {
		values[variable.Key] = variable.Value
	}
	return values
}

type testModelVariable struct {
	Key         string  `json:"key"`
	Workflow    string  `json:"workflow"`
	Input       string  `json:"input"`
	Provider    string  `json:"provider"`
	Value       string  `json:"value"`
	Description string  `json:"description"`
	Preset      string  `json:"preset"`
	Override    *string `json:"override"`
	Conflict    string  `json:"conflict"`
}

// Longest accepted model identifier.
const maxTestModelIDLength = 200

type testModelList struct {
	Variables         []testModelVariable `json:"variables"`
	Problems          []string            `json:"problems"`
	Available         []string            `json:"available"`
	Presets           map[string]string   `json:"presets"`
	MigrationRequired bool                `json:"migration_required"`
}

func getModels(t *testing.T, d *dash) testModelList {
	t.Helper()
	if d.modelCatalog == nil {
		d.modelCatalog = func(context.Context) []string { return []string{} }
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/models", nil)
	req.Host = "127.0.0.1:8099"
	d.models(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/models returned %d: %s", rec.Code, rec.Body)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("content type = %q", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("cache control = %q", got)
	}
	var out testModelList
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode GET /api/models: %v (%s)", err, rec.Body)
	}
	if out.Variables == nil {
		t.Fatal("GET /api/models returned a null variables array")
	}
	if out.Problems == nil {
		t.Fatal("GET /api/models returned a null problems array")
	}
	if out.Available == nil {
		t.Fatal("GET /api/models returned a null available array")
	}
	if out.Presets == nil {
		t.Fatal("GET /api/models returned a null presets object")
	}
	return out
}

func getModelVariables(t *testing.T, d *dash) []testModelVariable {
	t.Helper()
	return getModels(t, d).Variables
}

func putModelsRaw(d *dash, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPut, "/api/models", strings.NewReader(body))
	req.Host = "127.0.0.1:8099"
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	d.models(rec, req)
	return rec
}

func putModelValues(t *testing.T, d *dash, values map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]any{"variables": values})
	if err != nil {
		t.Fatal(err)
	}
	return putModelsRaw(d, string(body))
}

func putPresetUpdate(t *testing.T, d *dash, presets map[string]string, overrides map[string]*string) *httptest.ResponseRecorder {
	t.Helper()
	payload := map[string]any{"presets": presets}
	if overrides != nil {
		payload["overrides"] = overrides
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return putModelsRaw(d, string(body))
}

func testModelString(value string) *string { return &value }

func testMatchingModelValues() map[string]string {
	return map[string]string{
		"ask-pr/model":                   "provider/light",
		"fix-ci/model":                   "provider/light",
		"reply-pr/model":                 "provider/light",
		"review-pr/address_model":        "provider/light",
		"_fix-issue-owned/model":         "provider/default",
		"_fix-issue-external/model":      "provider/default",
		"tend-repo/model":                "provider/default",
		"review-pr/review_model":         "provider/default",
		"review-pr/claude_review_model":  "sonnet",
		"review-pr/claude_address_model": "haiku",
	}
}

func writeModelPresets(t *testing.T, root, body string) string {
	t.Helper()
	path := filepath.Join(root, "model-presets.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertModelChanges(t *testing.T, rec *httptest.ResponseRecorder, want []string) []models.Saved {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT /api/models returned %d: %s", rec.Code, rec.Body)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil || len(raw) != 1 || raw["changed"] == nil {
		t.Fatalf("response must contain only changed: %s (err %v)", rec.Body, err)
	}
	var out struct {
		Changed []models.Saved `json:"changed"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	got := make([]string, len(out.Changed))
	for i, change := range out.Changed {
		got[i] = change.Path
	}
	if !slices.Equal(got, want) {
		t.Fatalf("changed paths = %v, want %v", got, want)
	}
	return out.Changed
}

func assertModelFile(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("%s holds\n%q\nwant\n%q", path, got, want)
	}
}

func assertNoModelBackups(t *testing.T, root string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(root, "workflows", "*.bak"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("unexpected backups: %v", matches)
	}
}

func assertNoModelPresets(t *testing.T, root string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(root, "model-presets.yaml")); !os.IsNotExist(err) {
		t.Fatalf("unexpected preset file: %v", err)
	}
}

func TestModelsGETAvailable(t *testing.T) {
	root := t.TempDir()
	writeModelWorkflows(t, root, nil)
	want := []string{"provider/a#fast", "provider/z"}
	called := false
	d := &dash{root: root, modelCatalog: func(context.Context) []string {
		called = true
		return want
	}}

	out := getModels(t, d)
	if !called {
		t.Fatal("GET did not load the model catalog")
	}
	if len(out.Available) != len(want) {
		t.Fatalf("available = %q, want %q", out.Available, want)
	}
	for i := range want {
		if out.Available[i] != want[i] {
			t.Fatalf("available = %q, want %q", out.Available, want)
		}
	}
	if len(out.Variables) != len(testModelDefaults()) {
		t.Fatalf("got %d variables, want %d", len(out.Variables), len(testModelDefaults()))
	}
}

func TestModelsGETCatalogFailure(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	root := t.TempDir()
	writeModelWorkflows(t, root, nil)
	d := &dash{root: root}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/models", nil)
	req.Host = "127.0.0.1:8099"

	d.models(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET returned %d when the catalog command failed: %s", rec.Code, rec.Body)
	}
	var out testModelList
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Available == nil || len(out.Available) != 0 {
		t.Fatalf("available = %q, want an empty array", out.Available)
	}
	if len(out.Variables) != len(testModelDefaults()) {
		t.Fatalf("got %d variables, want %d", len(out.Variables), len(testModelDefaults()))
	}
}

func TestModelsGET(t *testing.T) {
	root := t.TempDir()
	writeModelWorkflows(t, root, map[string]string{
		"ask-pr/model":            `"provider/main"`,
		"review-pr/review_model":  "'provider/review#v1'",
		"review-pr/address_model": "provider/address",
	})
	d := &dash{root: root}

	variables := getModels(t, d).Variables
	want := []testModelVariable{
		{Key: "_fix-issue-external/model", Workflow: "_fix-issue-external", Input: "model", Provider: "opencode", Value: "provider/fix-issue", Description: "model for the fix agent", Preset: "default"},
		{Key: "_fix-issue-owned/model", Workflow: "_fix-issue-owned", Input: "model", Provider: "opencode", Value: "provider/fix-issue", Description: "model for the fix agent", Preset: "default"},
		{Key: "ask-pr/model", Workflow: "ask-pr", Input: "model", Provider: "opencode", Value: "provider/main", Description: "model for the ask agent", Preset: "light"},
		{Key: "fix-ci/model", Workflow: "fix-ci", Input: "model", Provider: "opencode", Value: "provider/fix-ci", Description: "model for the fix agent", Preset: "light", Override: testModelString("provider/fix-ci")},
		{Key: "reply-pr/model", Workflow: "reply-pr", Input: "model", Provider: "opencode", Value: "provider/reply", Description: "model for the reply agent", Preset: "light", Override: testModelString("provider/reply")},
		{Key: "review-pr/review_model", Workflow: "review-pr", Input: "review_model", Provider: "opencode", Value: "provider/review#v1", Description: "model for the review agent", Preset: "default", Override: testModelString("provider/review#v1")},
		{Key: "review-pr/address_model", Workflow: "review-pr", Input: "address_model", Provider: "opencode", Value: "provider/address", Description: "model for the address agent", Preset: "light", Override: testModelString("provider/address")},
		{Key: "review-pr/claude_review_model", Workflow: "review-pr", Input: "claude_review_model", Provider: "claude", Value: "sonnet", Description: "model for the claude_review_model agent", Preset: "claude_default"},
		{Key: "review-pr/claude_address_model", Workflow: "review-pr", Input: "claude_address_model", Provider: "claude", Value: "haiku", Description: "model for the claude_address_model agent", Preset: "claude_light"},
		{Key: "tend-repo/model", Workflow: "tend-repo", Input: "model", Provider: "opencode", Value: "provider/learn", Description: "model for the learn agent", Preset: "default", Override: testModelString("provider/learn")},
	}
	if len(variables) != len(want) {
		t.Fatalf("got %d variables, want %d: %+v", len(variables), len(want), variables)
	}
	for i := range want {
		got := variables[i]
		if !reflect.DeepEqual(got, want[i]) {
			t.Errorf("variable %d = %+v, want %+v", i, got, want[i])
		}
	}
	out := getModels(t, d)
	if !out.MigrationRequired {
		t.Fatal("missing preset file did not require migration")
	}
	if out.Presets["light"] != "provider/main" || out.Presets["default"] != "provider/fix-issue" {
		t.Fatalf("presets = %v", out.Presets)
	}
	if _, err := os.Stat(filepath.Join(root, "model-presets.yaml")); !os.IsNotExist(err) {
		t.Fatalf("GET created the preset file: %v", err)
	}
}

func TestModelsGETMigratesCurrentDefaults(t *testing.T) {
	const base = "opencode-go/hungerg-free"
	const address = "opencode-go/hy4-flash-free"
	values := map[string]string{}
	for key, value := range testMatchingModelValues() {
		values[key] = base
		if strings.Contains(key, "claude_") {
			values[key] = value // claude ids have their own syntax
		}
	}
	values["review-pr/address_model"] = address
	root := t.TempDir()
	originals := writeModelWorkflows(t, root, values)
	d := &dash{root: root}

	out := getModels(t, d)
	if !out.MigrationRequired {
		t.Fatal("missing preset file did not require migration")
	}
	if out.Presets["light"] != base || out.Presets["default"] != base {
		t.Fatalf("presets = %v, want both %q", out.Presets, base)
	}
	if len(out.Problems) != 0 || len(out.Variables) != len(values) {
		t.Fatalf("GET = %+v", out)
	}
	for _, variable := range out.Variables {
		wantPreset := "default"
		switch variable.Key {
		case "ask-pr/model", "fix-ci/model", "reply-pr/model", "review-pr/address_model":
			wantPreset = "light"
		case "review-pr/claude_review_model":
			wantPreset = "claude_default"
		case "review-pr/claude_address_model":
			wantPreset = "claude_light"
		}
		if variable.Preset != wantPreset {
			t.Fatalf("variable %s preset = %q, want %q", variable.Key, variable.Preset, wantPreset)
		}
		wantValue := base
		if strings.Contains(variable.Key, "claude_") {
			wantValue = values[variable.Key]
		} else if variable.Key == "review-pr/address_model" {
			wantValue = address
			if variable.Override == nil || *variable.Override != address {
				t.Fatalf("address override = %v, want %q", variable.Override, address)
			}
		} else if variable.Override != nil {
			t.Fatalf("unexpected override for %s: %q", variable.Key, *variable.Override)
		}
		if variable.Value != wantValue || variable.Conflict != "" {
			t.Fatalf("variable = %+v, want value %q without conflict", variable, wantValue)
		}
	}
	assertNoModelPresets(t, root)
	for file, want := range originals {
		assertModelFile(t, filepath.Join(root, "workflows", file), want)
	}

	overrides := map[string]*string{"review-pr/address_model": testModelString(address)}
	changes := assertModelChanges(t, putPresetUpdate(t, d, out.Presets, overrides), []string{"model-presets.yaml"})
	if changes[0].Backup != "" {
		t.Fatalf("new preset file has backup %q", changes[0].Backup)
	}
	for file, want := range originals {
		assertModelFile(t, filepath.Join(root, "workflows", file), want)
	}
	if got := currentModelValues(t, d); !maps.Equal(got, values) {
		t.Fatalf("migration changed runtime values: got %v, want %v", got, values)
	}
	out = getModels(t, d)
	if out.MigrationRequired || len(out.Problems) != 0 {
		t.Fatalf("GET after first save = %+v", out)
	}
	var addressVariable testModelVariable
	for _, variable := range out.Variables {
		if variable.Key == "review-pr/address_model" {
			addressVariable = variable
		}
	}
	if addressVariable.Key != "review-pr/address_model" || addressVariable.Override == nil || *addressVariable.Override != address {
		t.Fatalf("address variable after save = %+v", addressVariable)
	}
	assertModelChanges(t, putPresetUpdate(t, d, out.Presets, overrides), nil)
}

func TestModelsGETReportsProblemsWithoutVariables(t *testing.T) {
	t.Run("missing workflows", func(t *testing.T) {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, "workflows"), 0o755); err != nil {
			t.Fatal(err)
		}
		d := &dash{root: root}
		out := getModels(t, d)
		if len(out.Variables) != 0 {
			t.Fatalf("variables = %+v, want none", out.Variables)
		}
		if !slices.ContainsFunc(out.Problems, func(problem string) bool {
			return strings.Contains(problem, "missing workflow ask-pr.yaml")
		}) {
			t.Fatalf("problems = %v, want missing workflow errors", out.Problems)
		}
		if rec := putPresetUpdate(t, d, map[string]string{"light": "provider/light", "default": "provider/default"}, nil); rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("PUT returned %d, want 422: %s", rec.Code, rec.Body)
		}
		assertNoModelPresets(t, root)
		assertNoModelBackups(t, root)
	})

	t.Run("invalid config", func(t *testing.T) {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, "workflows"), 0o755); err != nil {
			t.Fatal(err)
		}
		const config = "version: [\n"
		path := writeModelPresets(t, root, config)
		d := &dash{root: root}
		out := getModels(t, d)
		if len(out.Variables) != 0 {
			t.Fatalf("variables = %+v, want none", out.Variables)
		}
		if !slices.ContainsFunc(out.Problems, func(problem string) bool {
			return strings.Contains(problem, "yaml")
		}) {
			t.Fatalf("problems = %v, want preset YAML error", out.Problems)
		}
		if rec := putPresetUpdate(t, d, map[string]string{"light": "provider/light", "default": "provider/default"}, nil); rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("PUT returned %d, want 422: %s", rec.Code, rec.Body)
		}
		assertModelFile(t, path, config)
		assertNoModelBackups(t, root)
	})
}

func TestModelsPUT(t *testing.T) {
	root := t.TempDir()
	originals := writeModelWorkflows(t, root, map[string]string{"ask-pr/model": "provider/old"})
	d := &dash{root: root}
	values := currentModelValues(t, d)
	values["ask-pr/model"] = "provider/new#fast"
	catalogCalled := false
	d.modelCatalog = func(context.Context) []string {
		catalogCalled = true
		return []string{"provider/new#fast"}
	}

	rec := putModelValues(t, d, values)
	changes := assertModelChanges(t, rec, []string{"workflows/ask-pr.yaml", "model-presets.yaml"})
	if catalogCalled {
		t.Fatal("PUT loaded the model catalog")
	}
	if changes[0].Backup == "" || changes[1].Backup != "" {
		t.Fatalf("changed = %+v, want a workflow backup and no preset backup", changes)
	}
	backup := changes[0].Backup
	if !strings.HasPrefix(backup, "ask-pr.yaml.") || !strings.HasSuffix(backup, ".bak") {
		t.Fatalf("backup name = %q", backup)
	}
	assertModelFile(t, filepath.Join(root, "workflows", "ask-pr.yaml"), strings.Replace(originals["ask-pr.yaml"], "provider/old", "provider/new#fast", 1))
	assertModelFile(t, filepath.Join(root, "workflows", "fix-ci.yaml"), originals["fix-ci.yaml"])
	assertModelFile(t, filepath.Join(root, "workflows", backup), originals["ask-pr.yaml"])
	backups, _ := filepath.Glob(filepath.Join(root, "workflows", "*.bak"))
	if len(backups) != 1 {
		t.Fatalf("got %d backups, want 1: %v", len(backups), backups)
	}

	snapshot, err := models.ReadConfigured(listFiles(filepath.Join(root, "workflows"), ".yaml"), d.resolveEditable, filepath.Join(root, "model-presets.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.MigrationRequired() {
		t.Fatal("preset file still requires migration after PUT")
	}
	if got := snapshot.Presets(); got["light"] != "provider/old" || got["default"] != "provider/fix-issue" {
		t.Fatalf("legacy PUT changed synthesized presets: %v", got)
	}
	configuration := snapshot.Configuration()
	if len(configuration.Overrides) != len(values) {
		t.Fatalf("legacy PUT overrides = %v, want every role", configuration.Overrides)
	}
	for key, value := range values {
		override := configuration.Overrides[key]
		if override == nil || *override != value {
			t.Errorf("override %s = %v, want %q", key, override, value)
		}
	}

	after := currentModelValues(t, d)
	if after["ask-pr/model"] != "provider/new#fast" || after["fix-ci/model"] != "provider/fix-ci" {
		t.Fatalf("GET after PUT returned %v", after)
	}
}

func TestModelsPUTPreservesYAML(t *testing.T) {
	root := t.TempDir()
	originals := writeModelWorkflows(t, root, map[string]string{
		"ask-pr/model":            "provider/old",
		"review-pr/review_model":  "'provider/review#v1'",
		"review-pr/address_model": `"provider/address"`,
	})
	for _, file := range []string{"ask-pr.yaml", "review-pr.yaml"} {
		body := strings.ReplaceAll(originals[file], "\n", "\r\n")
		body = strings.Replace(body, "# Keep the section comment.", "# Keep the section comment in "+file+".", 1)
		originals[file] = body
		if err := os.WriteFile(filepath.Join(root, "workflows", file), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	d := &dash{root: root}
	values := currentModelValues(t, d)
	values["ask-pr/model"] = "provider/team/model@stable:1#fast"
	values["review-pr/review_model"] = "openrouter/anthropic/claude-4.5#thinking"
	values["review-pr/address_model"] = "opencode-go/deepseek-v4.1-flash"

	rec := putModelValues(t, d, values)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT /api/models returned %d: %s", rec.Code, rec.Body)
	}
	changes := assertModelChanges(t, rec, []string{
		"workflows/ask-pr.yaml", "workflows/review-pr.yaml", "model-presets.yaml",
	})
	if changes[2].Backup != "" {
		t.Fatalf("new preset file has backup %q", changes[2].Backup)
	}
	for _, change := range changes[:2] {
		file := filepath.Base(change.Path)
		assertModelFile(t, filepath.Join(root, "workflows", change.Backup), originals[file])
	}
	wantAsk := strings.Replace(originals["ask-pr.yaml"], "provider/old", values["ask-pr/model"], 1)
	wantReview := strings.NewReplacer(
		"'provider/review#v1'", "'"+values["review-pr/review_model"]+"'",
		`"provider/address"`, `"`+values["review-pr/address_model"]+`"`,
	).Replace(originals["review-pr.yaml"])
	assertModelFile(t, filepath.Join(root, "workflows", "ask-pr.yaml"), wantAsk)
	assertModelFile(t, filepath.Join(root, "workflows", "review-pr.yaml"), wantReview)
	assertModelFile(t, filepath.Join(root, "workflows", "fix-ci.yaml"), originals["fix-ci.yaml"])
	backups, _ := filepath.Glob(filepath.Join(root, "workflows", "*.bak"))
	if len(backups) != 2 {
		t.Fatalf("got %d backups, want 2: %v", len(backups), backups)
	}
}

func TestModelsPUTUnchanged(t *testing.T) {
	root := t.TempDir()
	originals := writeModelWorkflows(t, root, nil)
	d := &dash{root: root}
	values := currentModelValues(t, d)

	changes := assertModelChanges(t, putModelValues(t, d, values), []string{"model-presets.yaml"})
	if changes[0].Backup != "" {
		t.Fatalf("new preset file has backup %q", changes[0].Backup)
	}
	for file, want := range originals {
		assertModelFile(t, filepath.Join(root, "workflows", file), want)
	}
	assertNoModelBackups(t, root)

	rec := putModelValues(t, d, values)
	if got := strings.TrimSpace(rec.Body.String()); got != `{"changed":[]}` {
		t.Fatalf("identical PUT returned %s, want an empty changed array", got)
	}
	assertNoModelBackups(t, root)
}

func TestModelsPUTModelLengthBoundary(t *testing.T) {
	root := t.TempDir()
	originals := writeModelWorkflows(t, root, map[string]string{"ask-pr/model": "provider/old"})
	d := &dash{root: root}
	value := strings.Repeat("a", 198) + "/b"
	if len(value) != maxTestModelIDLength {
		t.Fatalf("test value has %d characters", len(value))
	}
	values := currentModelValues(t, d)
	values["ask-pr/model"] = value
	if rec := putModelValues(t, d, values); rec.Code != http.StatusOK {
		t.Fatalf("200-character model returned %d: %s", rec.Code, rec.Body)
	}
	assertModelFile(t, filepath.Join(root, "workflows", "ask-pr.yaml"), strings.Replace(originals["ask-pr.yaml"], "provider/old", value, 1))
}

func TestModelsPUTRejectsInvalidIDs(t *testing.T) {
	invalid := []string{
		"",
		"provider",
		"/model",
		"provider/",
		"provider//model",
		"provider/model space",
		" provider/model",
		"provider/model ",
		"provider/model\nother",
		"provider/mödel",
		"provider/model#",
		"provider/model#bad/variant",
		"provider/model#variant#two",
		strings.Repeat("a", 199) + "/b",
	}
	for _, value := range invalid {
		t.Run(value, func(t *testing.T) {
			root := t.TempDir()
			originals := writeModelWorkflows(t, root, nil)
			d := &dash{root: root}
			values := currentModelValues(t, d)
			values["ask-pr/model"] = value
			if rec := putModelValues(t, d, values); rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("invalid model %q returned %d: %s", value, rec.Code, rec.Body)
			}
			for file, want := range originals {
				assertModelFile(t, filepath.Join(root, "workflows", file), want)
			}
			assertNoModelBackups(t, root)
		})
	}
}

func TestModelsPUTRejectsBadRequests(t *testing.T) {
	cases := []struct {
		name string
		body string
		want int
	}{
		{"malformed", `{"variables":`, http.StatusBadRequest},
		{"empty", ``, http.StatusBadRequest},
		{"missing payload", `{}`, http.StatusBadRequest},
		{"null variables", `{"variables":null}`, http.StatusBadRequest},
		{"array variables", `{"variables":[]}`, http.StatusBadRequest},
		{"non-string value", `{"variables":{"ask-pr/model":42}}`, http.StatusBadRequest},
		{"null value", `{"variables":{"ask-pr/model":null}}`, http.StatusBadRequest},
		{"both payloads", `{"variables":{},"presets":{"light":"provider/l","default":"provider/d"}}`, http.StatusBadRequest},
		{"variables and overrides", `{"variables":{},"overrides":{}}`, http.StatusBadRequest},
		{"unknown field", `{"presets":{"light":"provider/l","default":"provider/d"},"extra":true}`, http.StatusBadRequest},
		{"null presets", `{"presets":null}`, http.StatusBadRequest},
		{"array presets", `{"presets":[]}`, http.StatusBadRequest},
		{"null preset", `{"presets":{"light":null,"default":"provider/d"}}`, http.StatusBadRequest},
		{"non-string preset", `{"presets":{"light":42,"default":"provider/d"}}`, http.StatusBadRequest},
		{"null overrides", `{"presets":{"light":"provider/l","default":"provider/d"},"overrides":null}`, http.StatusBadRequest},
		{"array overrides", `{"presets":{"light":"provider/l","default":"provider/d"},"overrides":[]}`, http.StatusBadRequest},
		{"non-string override", `{"presets":{"light":"provider/l","default":"provider/d"},"overrides":{"ask-pr/model":42}}`, http.StatusBadRequest},
		{"missing preset", `{"presets":{"light":"provider/l"}}`, http.StatusUnprocessableEntity},
		{"unknown preset", `{"presets":{"light":"provider/l","default":"provider/d","other":"provider/o"}}`, http.StatusUnprocessableEntity},
		{"unknown override", `{"presets":{"light":"provider/l","default":"provider/d"},"overrides":{"other/model":"provider/o"}}`, http.StatusUnprocessableEntity},
		{"trailing object", `{"variables":{"ask-pr/model":"provider/new"}} {}`, http.StatusBadRequest},
		{"unknown variable", `{"variables":{"other/model":"provider/new"}}`, http.StatusUnprocessableEntity},
		{"missing variable", `{"variables":{}}`, http.StatusUnprocessableEntity},
		{"oversized", strings.Repeat(" ", maxModelsBodySize+1), http.StatusRequestEntityTooLarge},
		{"oversized trailing body", `{}` + strings.Repeat(" ", maxModelsBodySize+1), http.StatusRequestEntityTooLarge},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			originals := writeModelWorkflows(t, root, nil)
			d := &dash{root: root}
			if rec := putModelsRaw(d, c.body); rec.Code != c.want {
				t.Fatalf("returned %d, want %d: %s", rec.Code, c.want, rec.Body)
			}
			for file, want := range originals {
				assertModelFile(t, filepath.Join(root, "workflows", file), want)
			}
			assertNoModelBackups(t, root)
		})
	}
}

func TestModelsRejectsMethodAndUntrustedRequests(t *testing.T) {
	cases := []struct {
		name        string
		method      string
		contentType string
		host        string
		origin      string
		want        int
	}{
		{"post", http.MethodPost, "application/json", "127.0.0.1:8099", "", http.StatusMethodNotAllowed},
		{"delete", http.MethodDelete, "application/json", "127.0.0.1:8099", "", http.StatusMethodNotAllowed},
		{"plain body", http.MethodPut, "text/plain", "127.0.0.1:8099", "", http.StatusUnsupportedMediaType},
		{"json suffix", http.MethodPut, "application/jsonp", "127.0.0.1:8099", "", http.StatusUnsupportedMediaType},
		{"missing content type", http.MethodPut, "", "127.0.0.1:8099", "", http.StatusUnsupportedMediaType},
		{"untrusted put host", http.MethodPut, "application/json", "evil.example", "", http.StatusForbidden},
		{"untrusted put origin", http.MethodPut, "application/json", "127.0.0.1:8099", "http://evil.example", http.StatusForbidden},
		{"untrusted get host", http.MethodGet, "", "evil.example", "", http.StatusForbidden},
		{"untrusted get origin", http.MethodGet, "", "127.0.0.1:8099", "http://evil.example", http.StatusForbidden},
		{"json charset", http.MethodPut, "application/json; charset=utf-8", "127.0.0.1:8099", "http://127.0.0.1:8099", http.StatusOK},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			originals := writeModelWorkflows(t, root, nil)
			d := &dash{root: root}
			encoded, err := json.Marshal(map[string]any{"variables": currentModelValues(t, d)})
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(c.method, "/api/models", strings.NewReader(string(encoded)))
			req.Host = c.host
			if c.contentType != "" {
				req.Header.Set("Content-Type", c.contentType)
			}
			if c.origin != "" {
				req.Header.Set("Origin", c.origin)
			}
			rec := httptest.NewRecorder()
			d.models(rec, req)
			if rec.Code != c.want {
				t.Fatalf("returned %d, want %d: %s", rec.Code, c.want, rec.Body)
			}
			for file, want := range originals {
				assertModelFile(t, filepath.Join(root, "workflows", file), want)
			}
			assertNoModelBackups(t, root)
		})
	}
}

func TestModelsStructuralConflicts(t *testing.T) {
	duplicate := "  - name: model\n" +
		"    type: string\n" +
		"    required: false\n" +
		"    default: provider/other\n" +
		"    validation:\n" +
		"      pattern: '" + models.Pattern + "'\n"
	cases := []struct {
		name   string
		mutate func(string) string
		want   string
	}{
		{"duplicate name", func(s string) string { return strings.Replace(s, "states:\n", duplicate+"states:\n", 1) }, "duplicate input name"},
		{"missing default", func(s string) string {
			return strings.Replace(s, "    default: provider/old # keep this comment\n", "", 1)
		}, "missing default"},
		{"wrong type", func(s string) string {
			return strings.Replace(s, "  - name: model\n    type: string\n", "  - name: model\n    type: number\n", 1)
		}, "type must be string"},
		{"required", func(s string) string {
			return strings.Replace(s, "  - name: model\n    type: string\n    required: false\n", "  - name: model\n    type: string\n    required: true\n", 1)
		}, "required must be false"},
		{"missing required", func(s string) string {
			return strings.Replace(s, "  - name: model\n    type: string\n    required: false\n", "  - name: model\n    type: string\n", 1)
		}, "required must be false"},
		{"wrong pattern", func(s string) string { return strings.Replace(s, models.Pattern, `^.*$`, 1) }, "validation pattern"},
		{"pinned model", func(s string) string {
			return strings.Replace(s, `model: "{{.inputs.model}}"`, `model: "provider/literal"`, 1)
		}, `must use "{{.inputs.model}}"`},
		{"literal default", func(s string) string {
			return strings.Replace(s, "    default: provider/old # keep this comment\n", "    default: |-\n      provider/old\n", 1)
		}, "unsupported default scalar style"},
		{"folded default", func(s string) string {
			return strings.Replace(s, "    default: provider/old # keep this comment\n", "    default: >-\n      provider/old\n", 1)
		}, "unsupported default scalar style"},
		{"non-string default", func(s string) string {
			return strings.Replace(s, "    default: provider/old # keep this comment\n", "    default: 123\n", 1)
		}, "default must be a string scalar"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			originals := writeModelWorkflows(t, root, map[string]string{"ask-pr/model": "provider/old"})
			body := c.mutate(originals["ask-pr.yaml"])
			path := filepath.Join(root, "workflows", "ask-pr.yaml")
			if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			d := &dash{root: root}
			variables := getModelVariables(t, d)
			found := false
			for _, variable := range variables {
				if strings.Contains(variable.Conflict, c.want) {
					found = true
				}
			}
			if !found {
				t.Fatalf("no conflict contains %q: %+v", c.want, variables)
			}
			if rec := putModelValues(t, d, currentModelValues(t, d)); rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("PUT returned %d, want 422: %s", rec.Code, rec.Body)
			}
			assertModelFile(t, path, body)
			assertNoModelBackups(t, root)
		})
	}
}

func TestModelsEnforcesRoleMapping(t *testing.T) {
	extraInput := "  - name: other_model\n" +
		"    type: string\n" +
		"    required: false\n" +
		"    default: provider/extra\n" +
		"    validation:\n" +
		"      pattern: '" + models.Pattern + "'\n"
	extraState := "  extra:\n" +
		"    type: agent\n" +
		"    options:\n" +
		"      model: \"{{.inputs.model}}\"\n"
	cases := []struct {
		name   string
		file   string
		mutate func(string) string
		remove bool
		want   string
	}{
		{"missing input", "ask-pr.yaml", func(s string) string {
			before, _, _ := strings.Cut(s, "  - name: model\n")
			end := strings.Index(s, "\nstates:\n")
			return before + s[end+1:]
		}, false, `missing model input "model"`},
		{"missing workflow", "ask-pr.yaml", nil, true, "missing workflow ask-pr.yaml"},
		{"mismatched workflow name", "ask-pr.yaml", func(s string) string {
			return strings.Replace(s, "name: ask-pr\n", "name: renamed\n", 1)
		}, false, `workflow name must match filename stem "ask-pr"`},
		{"extra input", "ask-pr.yaml", func(s string) string {
			return strings.Replace(s, "states:\n", extraInput+"states:\n", 1)
		}, false, `unexpected model input "other_model"`},
		{"extra state", "ask-pr.yaml", func(s string) string {
			return strings.Replace(s, "  done:\n", extraState+"  done:\n", 1)
		}, false, `unexpected agent state "extra"`},
		{"renamed state", "ask-pr.yaml", func(s string) string {
			return strings.Replace(s, "  ask:\n", "  renamed:\n", 1)
		}, false, `missing agent state "ask"`},
		{"renamed input", "ask-pr.yaml", func(s string) string {
			return strings.Replace(s, "  - name: model\n", "  - name: renamed_model\n", 1)
		}, false, `missing model input "model"`},
		{"pinned state", "ask-pr.yaml", func(s string) string {
			return strings.Replace(s, `model: "{{.inputs.model}}"`, `model: "provider/literal"`, 1)
		}, false, `must use "{{.inputs.model}}"`},
		{"leading template whitespace", "ask-pr.yaml", func(s string) string {
			return strings.Replace(s, `model: "{{.inputs.model}}"`, `model: " {{.inputs.model}}"`, 1)
		}, false, `must use "{{.inputs.model}}"`},
		{"trailing template whitespace", "ask-pr.yaml", func(s string) string {
			return strings.Replace(s, `model: "{{.inputs.model}}"`, `model: "{{.inputs.model}} "`, 1)
		}, false, `must use "{{.inputs.model}}"`},
		{"swapped review roles", "review-pr.yaml", func(s string) string {
			return strings.NewReplacer(
				`model: "{{.states.pick_review_model.Output}}"`, `model: "{{.states.pick_address_model.Output}}"`,
				`model: "{{.states.pick_address_model.Output}}"`, `model: "{{.states.pick_review_model.Output}}"`,
			).Replace(s)
		}, false, `agent state "review" must use "{{.states.pick_review_model.Output}}"`},
		{"claude input with the opencode pattern", "review-pr.yaml", func(s string) string {
			return strings.Replace(s, models.ClaudePattern, models.Pattern, 1)
		}, false, "validation pattern must match the model pattern of its provider"},
		{"pick step ignores the claude model", "review-pr.yaml", func(s string) string {
			return strings.Replace(s, "{{.inputs.claude_review_model}}'", "'", 1)
		}, false, `step "pick_review_model" must read {{.inputs.claude_review_model}}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			originals := writeModelWorkflows(t, root, nil)
			path := filepath.Join(root, "workflows", c.file)
			if c.remove {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			} else {
				body := c.mutate(originals[c.file])
				if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
				originals[c.file] = body
			}
			d := &dash{root: root, modelCatalog: func(context.Context) []string { return []string{} }}
			out := getModels(t, d)
			found := slices.ContainsFunc(out.Problems, func(problem string) bool {
				return strings.Contains(problem, c.want)
			})
			for _, variable := range out.Variables {
				if strings.Contains(variable.Conflict, c.want) {
					found = true
				}
			}
			if !found {
				t.Fatalf("no problem or conflict contains %q: %+v", c.want, out)
			}
			if rec := putModelValues(t, d, testModelDefaults()); rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("PUT returned %d, want 422: %s", rec.Code, rec.Body)
			}
			for file, want := range originals {
				if c.remove && file == c.file {
					continue
				}
				assertModelFile(t, filepath.Join(root, "workflows", file), want)
			}
			assertNoModelBackups(t, root)
		})
	}
}

func TestModelsPUTRejectsEqualReviewAndAddressModels(t *testing.T) {
	root := t.TempDir()
	originals := writeModelWorkflows(t, root, nil)
	d := &dash{root: root}
	values := currentModelValues(t, d)
	values["review-pr/review_model"] = "provider/same"
	values["review-pr/address_model"] = "provider/same"

	rec := putModelValues(t, d, values)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "must be different") {
		t.Fatalf("PUT returned %d: %s, want 422 for equal review/address models", rec.Code, rec.Body)
	}
	for file, want := range originals {
		assertModelFile(t, filepath.Join(root, "workflows", file), want)
	}
	assertNoModelBackups(t, root)
}

func TestModelsPUTPreservesFlowStyleDefaults(t *testing.T) {
	root := t.TempDir()
	originals := writeModelWorkflows(t, root, nil)
	ask := "name: ask-pr\n" +
		"inputs: [{name: token, type: string, required: true}, {name: model, type: string, required: false, description: flow model, validation: {pattern: '" + models.Pattern + "'}, default: provider/old}]\n" +
		"states: {initial: ask, ask: {type: agent, provider: opencode, options: {model: \"{{.inputs.model}}\"}, prompt: \"Keep this prompt.\"}, done: {type: terminal, status: success}, error: {type: terminal, status: failure}}\n"
	review := "name: review-pr\n" +
		"inputs: [{name: claude_review_model, type: string, required: false, default: sonnet, validation: {pattern: '" + models.ClaudePattern + "'}}, {name: claude_address_model, type: string, required: false, default: haiku, validation: {pattern: '" + models.ClaudePattern + "'}}, {name: provider, type: string, required: false, default: opencode}, {name: review_model, type: string, required: false, default: provider/review, validation: {pattern: '" + models.Pattern + "'}}, {name: address_model, type: string, required: false, default: provider/address, validation: {pattern: '" + models.Pattern + "'}}]\n" +
		"states: {initial: pick_review_model, pick_review_model: {type: step, command: \"echo {{.inputs.provider}} {{.inputs.review_model}} {{.inputs.claude_review_model}}\"}, pick_address_model: {type: step, command: \"echo {{.inputs.provider}} {{.inputs.address_model}} {{.inputs.claude_address_model}}\"}, review: {type: agent, options: {model: \"{{.states.pick_review_model.Output}}\"}}, address: {type: agent, options: {model: \"{{.states.pick_address_model.Output}}\"}}, done: {type: terminal}, error: {type: terminal}}\n"
	for file, body := range map[string]string{"ask-pr.yaml": ask, "review-pr.yaml": review} {
		if err := os.WriteFile(filepath.Join(root, "workflows", file), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		originals[file] = body
	}
	d := &dash{root: root}
	values := currentModelValues(t, d)
	values["ask-pr/model"] = "provider/new#fast"
	values["review-pr/review_model"] = "provider/reviewer"
	values["review-pr/address_model"] = "provider/fixer"

	rec := putModelValues(t, d, values)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT /api/models returned %d: %s", rec.Code, rec.Body)
	}
	assertModelFile(t, filepath.Join(root, "workflows", "ask-pr.yaml"), strings.Replace(ask, "default: provider/old}", "default: "+values["ask-pr/model"]+"}", 1))
	wantReview := strings.NewReplacer(
		"default: provider/review,", "default: "+values["review-pr/review_model"]+",",
		"default: provider/address,", "default: "+values["review-pr/address_model"]+",",
	).Replace(review)
	assertModelFile(t, filepath.Join(root, "workflows", "review-pr.yaml"), wantReview)
	assertModelFile(t, filepath.Join(root, "workflows", "fix-ci.yaml"), originals["fix-ci.yaml"])
}

func TestModelsGETInvalidCurrentDefaults(t *testing.T) {
	cases := []struct {
		name           string
		defaultLiteral string
		wantValue      string
		wantConflict   string
	}{
		{"empty", `""`, "", "default must be a non-empty model identifier"},
		{"invalid", "not-a-model", "not-a-model", "default is not a valid model identifier"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			originals := writeModelWorkflows(t, root, map[string]string{"ask-pr/model": c.defaultLiteral})
			d := &dash{root: root}

			variables := getModelVariables(t, d)
			if len(variables) != len(testModelDefaults()) {
				t.Fatalf("got %d variables, want %d: %+v", len(variables), len(testModelDefaults()), variables)
			}
			var ask testModelVariable
			for _, variable := range variables {
				if variable.Key == "ask-pr/model" {
					ask = variable
				}
			}
			if ask.Value != c.wantValue {
				t.Errorf("ask-pr/model = %+v, want value %q", ask, c.wantValue)
			}
			if !strings.Contains(ask.Conflict, c.wantConflict) {
				t.Errorf("conflict = %q, want it to contain %q", ask.Conflict, c.wantConflict)
			}
			if rec := putModelValues(t, d, currentModelValues(t, d)); rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("PUT returned %d, want 422: %s", rec.Code, rec.Body)
			}
			for file, want := range originals {
				assertModelFile(t, filepath.Join(root, "workflows", file), want)
			}
			assertNoModelBackups(t, root)
		})
	}
}

func TestModelsPUTPreflightsEveryFile(t *testing.T) {
	root := t.TempDir()
	originals := writeModelWorkflows(t, root, map[string]string{
		"ask-pr/model": "provider/old",
		"fix-ci/model": "provider/old",
	})
	second := strings.Replace(originals["fix-ci.yaml"], "    default: provider/old # keep this comment\n", "", 1)
	if err := os.WriteFile(filepath.Join(root, "workflows", "fix-ci.yaml"), []byte(second), 0o644); err != nil {
		t.Fatal(err)
	}
	d := &dash{root: root}
	values := testModelDefaults()
	values["ask-pr/model"] = "provider/new"
	values["fix-ci/model"] = "provider/new"

	rec := putModelValues(t, d, values)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("PUT returned %d, want 422: %s", rec.Code, rec.Body)
	}
	for file, want := range originals {
		if file == "fix-ci.yaml" {
			want = second
		}
		assertModelFile(t, filepath.Join(root, "workflows", file), want)
	}
	assertNoModelBackups(t, root)
	assertNoModelPresets(t, root)
}

func TestModelsPUTPropagatesPresets(t *testing.T) {
	root := t.TempDir()
	writeModelWorkflows(t, root, testMatchingModelValues())
	d := &dash{root: root}
	presets := map[string]string{"light": "provider/new-light#fast", "default": "provider/new-default"}

	changes := assertModelChanges(t, putPresetUpdate(t, d, presets, nil), []string{
		"workflows/_fix-issue-external.yaml",
		"workflows/_fix-issue-owned.yaml",
		"workflows/ask-pr.yaml",
		"workflows/fix-ci.yaml",
		"workflows/reply-pr.yaml",
		"workflows/review-pr.yaml",
		"workflows/tend-repo.yaml",
		"model-presets.yaml",
	})
	for _, change := range changes[:7] {
		if change.Backup == "" {
			t.Fatalf("workflow change has no backup: %+v", change)
		}
	}
	if changes[7].Backup != "" {
		t.Fatalf("new preset file has backup %q", changes[6].Backup)
	}

	want := map[string]string{}
	for key, value := range testMatchingModelValues() {
		if strings.Contains(key, "claude_") {
			want[key] = value // claude models follow their own presets
		} else if value == "provider/light" {
			want[key] = presets["light"]
		} else {
			want[key] = presets["default"]
		}
	}
	if got := currentModelValues(t, d); !maps.Equal(got, want) {
		t.Fatalf("resolved values = %v, want %v", got, want)
	}
	out := getModels(t, d)
	if out.MigrationRequired || out.Presets["light"] != presets["light"] || out.Presets["default"] != presets["default"] {
		t.Fatalf("GET after PUT = %+v", out)
	}
	for _, variable := range out.Variables {
		if variable.Conflict != "" {
			t.Fatalf("unexpected conflict after propagation: %+v", variable)
		}
	}
}

func TestModelsPUTOverrideRetentionAndClearing(t *testing.T) {
	root := t.TempDir()
	writeModelWorkflows(t, root, testMatchingModelValues())
	d := &dash{root: root}
	presets := map[string]string{"light": "provider/light-2", "default": "provider/default"}

	assertModelChanges(t, putPresetUpdate(t, d, presets, map[string]*string{
		"ask-pr/model": testModelString("provider/custom"),
	}), []string{
		"workflows/ask-pr.yaml",
		"workflows/fix-ci.yaml",
		"workflows/reply-pr.yaml",
		"workflows/review-pr.yaml",
		"model-presets.yaml",
	})

	presets["light"] = "provider/light-3"
	assertModelChanges(t, putPresetUpdate(t, d, presets, map[string]*string{}), []string{
		"workflows/fix-ci.yaml",
		"workflows/reply-pr.yaml",
		"workflows/review-pr.yaml",
		"model-presets.yaml",
	})
	values := currentModelValues(t, d)
	if values["ask-pr/model"] != "provider/custom" {
		t.Fatalf("missing override key did not preserve ask-pr/model: %v", values)
	}
	if values["fix-ci/model"] != presets["light"] {
		t.Fatalf("light preset did not propagate: %v", values)
	}

	assertModelChanges(t, putPresetUpdate(t, d, presets, map[string]*string{
		"ask-pr/model": nil,
	}), []string{"workflows/ask-pr.yaml", "model-presets.yaml"})
	values = currentModelValues(t, d)
	if values["ask-pr/model"] != presets["light"] {
		t.Fatalf("cleared override did not resolve to the light preset: %v", values)
	}
	snapshot, err := models.ReadConfigured(listFiles(filepath.Join(root, "workflows"), ".yaml"), d.resolveEditable, filepath.Join(root, "model-presets.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if override := snapshot.Configuration().Overrides["ask-pr/model"]; override != nil {
		t.Fatalf("cleared override remains: %q", *override)
	}
}

func TestModelsGETDriftRefusesPUT(t *testing.T) {
	root := t.TempDir()
	originals := writeModelWorkflows(t, root, testMatchingModelValues())
	config := "version: 1\npresets:\n  light: provider/light\n  default: provider/drift\n"
	path := writeModelPresets(t, root, config)
	d := &dash{root: root}

	out := getModels(t, d)
	found := false
	for _, variable := range out.Variables {
		if strings.Contains(variable.Conflict, "differs from resolved model") {
			found = true
		}
	}
	if !found {
		t.Fatalf("GET did not report drift: %+v", out.Variables)
	}
	rec := putPresetUpdate(t, d, map[string]string{"light": "provider/light", "default": "provider/drift"}, nil)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "differs from resolved model") {
		t.Fatalf("PUT returned %d: %s, want 422 for drift", rec.Code, rec.Body)
	}
	assertModelFile(t, path, config)
	for file, want := range originals {
		assertModelFile(t, filepath.Join(root, "workflows", file), want)
	}
	assertNoModelBackups(t, root)
}

func TestModelsGETInvalidPresetConfig(t *testing.T) {
	valid := "version: 1\npresets:\n  light: provider/light\n  default: provider/default\n"
	cases := []struct {
		name string
		body string
		want string
	}{
		{"empty", "", "preset file is empty"},
		{"malformed", "version: [", "yaml"},
		{"multiple documents", valid + "---\n" + valid, "one YAML document"},
		{"unknown root key", valid + "extra: true\n", "field extra not found"},
		{"unsupported version", strings.Replace(valid, "version: 1", "version: 2", 1), "unsupported preset version"},
		{"string version", strings.Replace(valid, "version: 1", `version: "1"`, 1), "cannot unmarshal"},
		{"missing preset", "version: 1\npresets:\n  light: provider/light\n", "light and default, and may add"},
		{"unknown preset", valid + "  other: provider/other\n", "light and default, and may add"},
		{"duplicate preset", "version: 1\npresets:\n  light: provider/light\n  light: provider/other\n  default: provider/default\n", "already defined"},
		{"unknown role", valid + "overrides:\n  other/model: provider/other\n", "unknown model role"},
		{"null unknown role", valid + "overrides:\n  other/model: null\n", "unknown model role"},
		{"invalid override", valid + "overrides:\n  ask-pr/model: not-a-model\n", "model identifier is not valid"},
		{"equal review roles", "version: 1\npresets:\n  light: provider/same\n  default: provider/same\n", "must be different"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			originals := writeModelWorkflows(t, root, testMatchingModelValues())
			path := writeModelPresets(t, root, c.body)
			d := &dash{root: root}

			out := getModels(t, d)
			found := slices.ContainsFunc(out.Problems, func(problem string) bool {
				return strings.Contains(problem, c.want)
			})
			if !found {
				t.Fatalf("no problem contains %q: %+v", c.want, out.Problems)
			}
			rec := putPresetUpdate(t, d, map[string]string{"light": "provider/light", "default": "provider/default"}, nil)
			if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), c.want) {
				t.Fatalf("PUT returned %d: %s, want 422 containing %q", rec.Code, rec.Body, c.want)
			}
			assertModelFile(t, path, c.body)
			for file, want := range originals {
				assertModelFile(t, filepath.Join(root, "workflows", file), want)
			}
			assertNoModelBackups(t, root)
		})
	}
}

func TestModelsPUTRejectsInvalidPresetUpdates(t *testing.T) {
	cases := []struct {
		name      string
		presets   map[string]string
		overrides map[string]*string
		want      string
	}{
		{"invalid preset", map[string]string{"light": "not-a-model", "default": "provider/default"}, nil, "model identifier is not valid"},
		{"long preset", map[string]string{"light": strings.Repeat("a", 199) + "/b", "default": "provider/default"}, nil, "200"},
		{"invalid override", map[string]string{"light": "provider/light", "default": "provider/default"}, map[string]*string{"ask-pr/model": testModelString("")}, "model identifier is not valid"},
		{"equal review roles", map[string]string{"light": "provider/same", "default": "provider/same"}, nil, "must be different"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			originals := writeModelWorkflows(t, root, testMatchingModelValues())
			d := &dash{root: root}
			rec := putPresetUpdate(t, d, c.presets, c.overrides)
			if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), c.want) {
				t.Fatalf("PUT returned %d: %s, want 422 containing %q", rec.Code, rec.Body, c.want)
			}
			for file, want := range originals {
				assertModelFile(t, filepath.Join(root, "workflows", file), want)
			}
			assertNoModelBackups(t, root)
			assertNoModelPresets(t, root)
		})
	}
}

func TestModelsPUTRollsBackFailedPresetWrite(t *testing.T) {
	for _, created := range []bool{false, true} {
		name := "existing preset"
		if created {
			name = "new preset"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			originals := writeModelWorkflows(t, root, testMatchingModelValues())
			config := "version: 1\npresets:\n  light: provider/light\n  default: provider/default\n"
			path := filepath.Join(root, "model-presets.yaml")
			if !created {
				writeModelPresets(t, root, config)
			}
			d := &dash{root: root}
			originalApply := applyModelPlan
			applyModelPlan = func(plan *models.Plan) ([]models.Saved, error) {
				return plan.Apply(backupFile, func(path string, data []byte) error {
					if filepath.Base(path) == "model-presets.yaml" {
						if err := os.WriteFile(path, data, 0o644); err != nil {
							return err
						}
						return errors.New("injected preset write failure")
					}
					return atomicWrite(path, data)
				})
			}
			defer func() { applyModelPlan = originalApply }()

			rec := putPresetUpdate(t, d, map[string]string{"light": "provider/new-light", "default": "provider/new-default"}, nil)
			if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "injected preset write failure") {
				t.Fatalf("PUT returned %d: %s, want rollback error", rec.Code, rec.Body)
			}
			for file, want := range originals {
				assertModelFile(t, filepath.Join(root, "workflows", file), want)
			}
			if created {
				assertNoModelPresets(t, root)
			} else {
				assertModelFile(t, path, config)
			}
		})
	}
}
