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

const presetsDoc = `version: 1
presets:
    light: acme/small
    default: acme/big
    claude_light: haiku
    claude_default: sonnet
overrides:
    review-pr/address_model: other/fixer
`

func modelsDash(t *testing.T) (*dash, string) {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, models.File)
	if err := os.WriteFile(path, []byte(presetsDoc), 0o644); err != nil {
		t.Fatal(err)
	}
	return &dash{root: root, modelCatalog: func(context.Context) []string { return []string{"acme/a"} }}, path
}

func callModels(d *dash, method, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/api/models", strings.NewReader(body))
	req.Host = "127.0.0.1:8099"
	if method == http.MethodPut {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	d.models(rec, req)
	return rec
}

func TestModelsGETListsRolesPresetsAndCatalog(t *testing.T) {
	d, _ := modelsDash(t)
	rec := callModels(d, http.MethodGet, "")
	var out struct {
		Variables []models.Variable
		Presets   map[string]string
		Available []string
	}
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil {
		t.Fatalf("GET = %d %s", rec.Code, rec.Body)
	}
	if len(out.Variables) != len(models.Roles) || out.Presets["light"] != "acme/small" || len(out.Available) != 1 {
		t.Fatalf("answer = %+v", out)
	}
	for _, v := range out.Variables {
		if v.Key == "review-pr/address_model" && (v.Override == nil || *v.Override != "other/fixer" || v.Value != "other/fixer") {
			t.Errorf("override not shown: %+v", v)
		}
	}
}

func TestModelsPUTWritesThePresetFileAndKeepsABackup(t *testing.T) {
	d, path := modelsDash(t)
	rec := callModels(d, http.MethodPut, `{"presets":{"light":"acme/tiny"},"overrides":{"review-pr/address_model":null,"ask-pr/model":"x/y#fast"}}`)
	if rec.Code != 200 {
		t.Fatalf("PUT = %d %s", rec.Code, rec.Body)
	}
	cfg, err := models.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Resolve("ask-pr/model") != "x/y#fast" || cfg.Resolve("fix-ci/model") != "acme/tiny" || cfg.Resolve("review-pr/address_model") != "acme/tiny" {
		t.Fatalf("saved = %+v", cfg)
	}
	if baks, _ := filepath.Glob(path + "*.bak"); len(baks) != 1 {
		t.Fatalf("backups = %v", baks)
	}
}

func TestModelsPUTRefusesAndLeavesTheFile(t *testing.T) {
	d, path := modelsDash(t)
	for name, c := range map[string]struct {
		body string
		code int
	}{
		"bad model":       {`{"presets":{"light":"nope"}}`, 422},
		"unknown preset":  {`{"presets":{"tiny":"a/b"}}`, 422},
		"unknown role":    {`{"overrides":{"ghost/model":"a/b"}}`, 422},
		"equal reviewers": {`{"overrides":{"review-pr/review_model":"other/fixer"}}`, 422},
		"unknown field":   {`{"variables":{}}`, 400},
		"not json":        {`{`, 400},
		"two objects":     {`{}{}`, 400},
		"too large":       {`{"presets":{"light":"` + strings.Repeat("a", maxModelsBodySize) + `"}}`, 413},
	} {
		if rec := callModels(d, http.MethodPut, c.body); rec.Code != c.code {
			t.Errorf("%s = %d, want %d (%s)", name, rec.Code, c.code, rec.Body)
		}
	}
	if raw, _ := os.ReadFile(path); string(raw) != presetsDoc {
		t.Fatalf("the file changed: %s", raw)
	}
}

func TestModelsRejectsWhatIsNotAnOwnerWriteOfJSON(t *testing.T) {
	d, _ := modelsDash(t)
	if rec := callModels(d, http.MethodPost, ""); rec.Code != 405 {
		t.Errorf("POST = %d", rec.Code)
	}
	req := httptest.NewRequest(http.MethodPut, "/api/models", strings.NewReader("{}"))
	req.Host = "evil.example"
	req.Header.Set("Content-Type", "application/json")
	if rec := httptest.NewRecorder(); func() int { d.models(rec, req); return rec.Code }() != 403 {
		t.Error("an untrusted host was accepted")
	}
	req = httptest.NewRequest(http.MethodPut, "/api/models", strings.NewReader("{}"))
	req.Host = "127.0.0.1:8099"
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	d.models(rec, req)
	if rec.Code != 415 {
		t.Errorf("text/plain = %d", rec.Code)
	}
}

func TestModelsGETWithoutThePresetFileIsAnError(t *testing.T) {
	d := &dash{root: t.TempDir()}
	if rec := callModels(d, http.MethodGet, ""); rec.Code != 500 {
		t.Fatalf("GET = %d", rec.Code)
	}
}
