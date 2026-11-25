package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"opencode-dots/internal/models"
)

type testModelInput struct {
	name  string
	state string
}

func testModelDefaults() map[string]string {
	return map[string]string{
		"ask-pr/model":            "provider/ask",
		"fix-ci/model":            "provider/fix-ci",
		"fix-issue/model":         "provider/fix-issue",
		"reply-pr/model":          "provider/reply",
		"review-pr/review_model":  "provider/review",
		"review-pr/address_model": "provider/address",
		"tend-repo/model":         "provider/learn",
	}
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
		{"fix-issue.yaml", []testModelInput{{name: "model", state: "fix"}}},
		{"reply-pr.yaml", []testModelInput{{name: "model", state: "reply"}}},
		{"review-pr.yaml", []testModelInput{{name: "review_model", state: "review"}, {name: "address_model", state: "address"}}},
		{"tend-repo.yaml", []testModelInput{{name: "model", state: "learn"}}},
	}
	originals := make(map[string]string, len(groups))
	for _, group := range groups {
		workflow := strings.TrimSuffix(group.file, ".yaml")
		var body strings.Builder
		body.WriteString("name: " + workflow + "\nversion: \"1.0.0\"\n\n# Keep the section comment.\ninputs:\n")
		body.WriteString("  - name: token\n    type: string\n    required: true\n")
		for _, input := range group.inputs {
			body.WriteString("  - name: " + input.name + "\n" +
				"    type: string\n" +
				"    required: false\n" +
				"    default: " + defaults[workflow+"/"+input.name] + " # keep this comment\n" +
				"    description: model for the " + input.state + " agent\n" +
				"    validation:\n" +
				"      pattern: '" + models.Pattern + "'\n")
		}
		body.WriteString("\nstates:\n  initial: " + group.inputs[0].state + "\n")
		for _, input := range group.inputs {
			body.WriteString("  " + input.state + ":\n" +
				"    type: agent\n" +
				"    provider: opencode\n" +
				"    options:\n" +
				"      model: \"{{.inputs." + input.name + "}}\"\n" +
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
	Key         string `json:"key"`
	Workflow    string `json:"workflow"`
	Input       string `json:"input"`
	Value       string `json:"value"`
	Description string `json:"description"`
	Conflict    string `json:"conflict"`
}

// Longest accepted model identifier.
const maxTestModelIDLength = 200

type testModelList struct {
	Variables []testModelVariable `json:"variables"`
	Available []string            `json:"available"`
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
	if out.Available == nil {
		t.Fatal("GET /api/models returned a null available array")
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

	variables := getModelVariables(t, d)
	want := []testModelVariable{
		{Key: "ask-pr/model", Workflow: "ask-pr", Input: "model", Value: "provider/main", Description: "model for the ask agent"},
		{Key: "fix-ci/model", Workflow: "fix-ci", Input: "model", Value: "provider/fix-ci", Description: "model for the fix agent"},
		{Key: "fix-issue/model", Workflow: "fix-issue", Input: "model", Value: "provider/fix-issue", Description: "model for the fix agent"},
		{Key: "reply-pr/model", Workflow: "reply-pr", Input: "model", Value: "provider/reply", Description: "model for the reply agent"},
		{Key: "review-pr/review_model", Workflow: "review-pr", Input: "review_model", Value: "provider/review#v1", Description: "model for the review agent"},
		{Key: "review-pr/address_model", Workflow: "review-pr", Input: "address_model", Value: "provider/address", Description: "model for the address agent"},
		{Key: "tend-repo/model", Workflow: "tend-repo", Input: "model", Value: "provider/learn", Description: "model for the learn agent"},
	}
	if len(variables) != len(want) {
		t.Fatalf("got %d variables, want %d: %+v", len(variables), len(want), variables)
	}
	for i := range want {
		got := variables[i]
		if got != want[i] {
			t.Errorf("variable %d = %+v, want %+v", i, got, want[i])
		}
	}
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
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT /api/models returned %d: %s", rec.Code, rec.Body)
	}
	if catalogCalled {
		t.Fatal("PUT loaded the model catalog")
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil || len(raw) != 1 {
		t.Fatalf("response must contain only changed: %s (err %v)", rec.Body, err)
	}
	var out struct {
		Changed []models.Saved `json:"changed"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Changed) != 1 || out.Changed[0].Path != "workflows/ask-pr.yaml" {
		t.Fatalf("changed = %+v, want only workflows/ask-pr.yaml", out.Changed)
	}
	backup := out.Changed[0].Backup
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

	variables := getModelVariables(t, d)
	if variables[0].Value != "provider/new#fast" || variables[1].Value != "provider/fix-ci" {
		t.Fatalf("GET after PUT returned %+v", variables)
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
	var out struct {
		Changed []models.Saved `json:"changed"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Changed) != 2 {
		t.Fatalf("changed = %+v, want two files", out.Changed)
	}
	for _, change := range out.Changed {
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

	rec := putModelValues(t, d, currentModelValues(t, d))
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT /api/models returned %d: %s", rec.Code, rec.Body)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != `{"changed":[]}` {
		t.Fatalf("identical PUT returned %s, want an empty changed array", got)
	}
	for file, want := range originals {
		assertModelFile(t, filepath.Join(root, "workflows", file), want)
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
		{"missing variables", `{}`, http.StatusBadRequest},
		{"null variables", `{"variables":null}`, http.StatusBadRequest},
		{"array variables", `{"variables":[]}`, http.StatusBadRequest},
		{"non-string value", `{"variables":{"ask-pr/model":42}}`, http.StatusBadRequest},
		{"null value", `{"variables":{"ask-pr/model":null}}`, http.StatusBadRequest},
		{"trailing object", `{"variables":{"ask-pr/model":"provider/new"}} {}`, http.StatusBadRequest},
		{"unknown key", `{"variables":{"other/model":"provider/new"}}`, http.StatusUnprocessableEntity},
		{"missing key", `{"variables":{}}`, http.StatusUnprocessableEntity},
		{"oversized", strings.Repeat(" ", maxModelsBodySize+1), http.StatusRequestEntityTooLarge},
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
		name       string
		file       string
		mutate     func(string) string
		remove     bool
		wantStatus int
		want       string
	}{
		{"missing input", "ask-pr.yaml", func(s string) string {
			start := strings.Index(s, "  - name: model\n")
			end := strings.Index(s, "\nstates:\n")
			return s[:start] + s[end+1:]
		}, false, http.StatusUnprocessableEntity, `missing model input "model"`},
		{"missing workflow", "ask-pr.yaml", nil, true, http.StatusUnprocessableEntity, "missing workflow ask-pr.yaml"},
		{"mismatched workflow name", "ask-pr.yaml", func(s string) string {
			return strings.Replace(s, "name: ask-pr\n", "name: renamed\n", 1)
		}, false, http.StatusOK, `workflow name must match filename stem "ask-pr"`},
		{"extra input", "ask-pr.yaml", func(s string) string {
			return strings.Replace(s, "states:\n", extraInput+"states:\n", 1)
		}, false, http.StatusOK, `unexpected model input "other_model"`},
		{"extra state", "ask-pr.yaml", func(s string) string {
			return strings.Replace(s, "  done:\n", extraState+"  done:\n", 1)
		}, false, http.StatusOK, `unexpected agent state "extra"`},
		{"renamed state", "ask-pr.yaml", func(s string) string {
			return strings.Replace(s, "  ask:\n", "  renamed:\n", 1)
		}, false, http.StatusOK, `missing agent state "ask"`},
		{"renamed input", "ask-pr.yaml", func(s string) string {
			return strings.Replace(s, "  - name: model\n", "  - name: renamed_model\n", 1)
		}, false, http.StatusOK, `missing model input "model"`},
		{"pinned state", "ask-pr.yaml", func(s string) string {
			return strings.Replace(s, `model: "{{.inputs.model}}"`, `model: "provider/literal"`, 1)
		}, false, http.StatusOK, `must use "{{.inputs.model}}"`},
		{"leading template whitespace", "ask-pr.yaml", func(s string) string {
			return strings.Replace(s, `model: "{{.inputs.model}}"`, `model: " {{.inputs.model}}"`, 1)
		}, false, http.StatusOK, `must use "{{.inputs.model}}"`},
		{"trailing template whitespace", "ask-pr.yaml", func(s string) string {
			return strings.Replace(s, `model: "{{.inputs.model}}"`, `model: "{{.inputs.model}} "`, 1)
		}, false, http.StatusOK, `must use "{{.inputs.model}}"`},
		{"swapped review roles", "review-pr.yaml", func(s string) string {
			return strings.NewReplacer(
				`model: "{{.inputs.review_model}}"`, `model: "{{.inputs.address_model}}"`,
				`model: "{{.inputs.address_model}}"`, `model: "{{.inputs.review_model}}"`,
			).Replace(s)
		}, false, http.StatusOK, `agent state "review" must use "{{.inputs.review_model}}"`},
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
			if c.wantStatus == http.StatusOK {
				found := false
				for _, variable := range getModelVariables(t, d) {
					if strings.Contains(variable.Conflict, c.want) {
						found = true
					}
				}
				if !found {
					t.Fatalf("no conflict contains %q", c.want)
				}
			} else {
				rec := httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodGet, "/api/models", nil)
				req.Host = "127.0.0.1:8099"
				d.models(rec, req)
				var out struct {
					Error string `json:"error"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
					t.Fatal(err)
				}
				if rec.Code != c.wantStatus || !strings.Contains(out.Error, c.want) {
					t.Fatalf("GET returned %d: %s, want %d containing %q", rec.Code, out.Error, c.wantStatus, c.want)
				}
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
		"inputs: [{name: review_model, type: string, required: false, default: provider/review, validation: {pattern: '" + models.Pattern + "'}}, {name: address_model, type: string, required: false, default: provider/address, validation: {pattern: '" + models.Pattern + "'}}]\n" +
		"states: {initial: review, review: {type: agent, options: {model: \"{{.inputs.review_model}}\"}}, address: {type: agent, options: {model: \"{{.inputs.address_model}}\"}}, done: {type: terminal}, error: {type: terminal}}\n"
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
			if variables[0].Key != "ask-pr/model" || variables[0].Value != c.wantValue {
				t.Errorf("first variable = %+v, want ask-pr/model with value %q", variables[0], c.wantValue)
			}
			if !strings.Contains(variables[0].Conflict, c.wantConflict) {
				t.Errorf("conflict = %q, want it to contain %q", variables[0].Conflict, c.wantConflict)
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
}
