package models

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestParseCatalog(t *testing.T) {
	cases := []struct {
		name string
		data string
		want []string
	}{
		{"empty", "", []string{}},
		{"blank lines", "\n \r\n\t\n", []string{}},
		{"trim sort and deduplicate", " provider/z \n\nprovider/a#fast\r\nprovider/z\n", []string{"provider/a#fast", "provider/z"}},
		{"invalid IDs", "provider\n/model\nprovider/\nprovider/model space\nprovider//model\nprovider/model#\nprovider/model#bad/variant\n", []string{}},
		{"slashes and variants", "openrouter/anthropic/claude-4.5#thinking\nopencode-go/deepseek-v4.1-flash\nlocal/helper#fast\nlocal/helper#fast\n", []string{"local/helper#fast", "opencode-go/deepseek-v4.1-flash", "openrouter/anthropic/claude-4.5#thinking"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseCatalog([]byte(c.data))
			if got == nil {
				t.Fatal("parser returned a nil array")
			}
			if len(got) != len(c.want) {
				t.Fatalf("catalog = %q, want %q", got, c.want)
			}
			for i := range c.want {
				if got[i] != c.want[i] {
					t.Fatalf("catalog = %q, want %q", got, c.want)
				}
			}
		})
	}
}

func TestLoadCatalog(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "opencode")
	output := " provider/z \nprovider/a#fast\nprovider/z\ninvalid\n"
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s' '"+output+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	got := LoadCatalog(context.Background())
	want := []string{"provider/a#fast", "provider/z"}
	if len(got) != len(want) {
		t.Fatalf("catalog = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("catalog = %q, want %q", got, want)
		}
	}
}

func TestLoadCatalogTimeout(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "opencode")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexec /bin/sleep 10\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	start := time.Now()
	got := LoadCatalog(context.Background())
	if got == nil || len(got) != 0 {
		t.Fatalf("timed-out catalog = %q, want an empty array", got)
	}
	if elapsed := time.Since(start); elapsed > catalogTimeout+2*time.Second {
		t.Fatalf("catalog lookup took %s, want the %s timeout", elapsed, catalogTimeout)
	}
}

func TestWriteAllRollsBackWrittenFiles(t *testing.T) {
	root := t.TempDir()
	var changes []fileChange
	var paths []string
	for _, name := range []string{"a.yaml", "b.yaml", "c.yaml"} {
		path := filepath.Join(root, name)
		original := []byte("name: original-" + name + "\n")
		if err := os.WriteFile(path, original, 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
		changes = append(changes, fileChange{path: "workflows/" + name, abs: path, original: original, data: []byte("name: changed-" + name + "\n")})
	}
	write := func(path string, data []byte) error {
		if path == paths[2] {
			return errors.New("injected write failure")
		}
		return os.WriteFile(path, data, 0o644)
	}
	err := writeAll(changes, write)
	if err == nil || !strings.Contains(err.Error(), "injected write failure") {
		t.Fatalf("writeAll error = %v", err)
	}
	for i, change := range changes {
		got, err := os.ReadFile(paths[i])
		if err != nil || string(got) != string(change.original) {
			t.Fatalf("%s holds %q (%v), want %q", paths[i], got, err, change.original)
		}
	}
}

// Every agent step must take its model from a defaulted, validated input.
func TestWorkflowModelInputs(t *testing.T) {
	type workflowInput struct {
		Name        string `yaml:"name"`
		Type        string `yaml:"type"`
		Required    *bool  `yaml:"required"`
		Default     string `yaml:"default"`
		Description string `yaml:"description"`
		Validation  struct {
			Pattern string `yaml:"pattern"`
		} `yaml:"validation"`
	}
	type workflowState struct {
		Type     string `yaml:"type"`
		Provider string `yaml:"provider"`
		Options  struct {
			Model string `yaml:"model"`
		} `yaml:"options"`
	}
	type workflowStates struct {
		Initial string                   `yaml:"initial"`
		Steps   map[string]workflowState `yaml:",inline"`
	}
	type workflowDocument struct {
		Name   string          `yaml:"name"`
		Inputs []workflowInput `yaml:"inputs"`
		States workflowStates  `yaml:"states"`
	}

	cases := []struct {
		file   string
		inputs map[string]bool
		agents map[string]string
	}{
		{
			file:   "ask-pr.yaml",
			inputs: map[string]bool{"model": true},
			agents: map[string]string{"ask": "{{.inputs.model}}"},
		},
		{
			file:   "fix-ci.yaml",
			inputs: map[string]bool{"model": true},
			agents: map[string]string{"fix": "{{.inputs.model}}"},
		},
		{
			file:   "fix-issue.yaml",
			inputs: map[string]bool{"model": true},
			agents: map[string]string{"fix": "{{.inputs.model}}"},
		},
		{
			file:   "reply-pr.yaml",
			inputs: map[string]bool{"model": true},
			agents: map[string]string{"reply": "{{.inputs.model}}"},
		},
		{
			file: "review-pr.yaml",
			inputs: map[string]bool{
				"review_model":  true,
				"address_model": true,
			},
			agents: map[string]string{
				"review":  "{{.inputs.review_model}}",
				"address": "{{.inputs.address_model}}",
			},
		},
		{
			file:   "tend-repo.yaml",
			inputs: map[string]bool{"model": true},
			agents: map[string]string{"learn": "{{.inputs.model}}"},
		},
	}

	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("..", "..", "workflows", c.file))
			if err != nil {
				t.Fatal(err)
			}
			var workflow workflowDocument
			if err := yaml.Unmarshal(data, &workflow); err != nil {
				t.Fatalf("parse workflow: %v", err)
			}
			if workflow.Name != strings.TrimSuffix(c.file, ".yaml") {
				t.Errorf("workflow name = %q, want %q", workflow.Name, strings.TrimSuffix(c.file, ".yaml"))
			}

			inputs := make(map[string]workflowInput)
			modelInputs := 0
			for _, input := range workflow.Inputs {
				if input.Name != "model" && !strings.HasSuffix(input.Name, "_model") {
					continue
				}
				modelInputs++
				inputs[input.Name] = input
			}
			if modelInputs != len(c.inputs) {
				t.Errorf("workflow has %d model inputs, want %d", modelInputs, len(c.inputs))
			}
			for name := range c.inputs {
				input, ok := inputs[name]
				if !ok {
					t.Errorf("missing model input %q", name)
					continue
				}
				if input.Type != "string" {
					t.Errorf("input %q type = %q, want string", name, input.Type)
				}
				if input.Required == nil || *input.Required {
					t.Errorf("input %q must set required: false", name)
				}
				if strings.TrimSpace(input.Default) == "" {
					t.Errorf("input %q has no default", name)
				}
				if strings.TrimSpace(input.Description) == "" {
					t.Errorf("input %q has no description", name)
				}
				if input.Validation.Pattern != Pattern {
					t.Errorf("input %q pattern = %q, want shared pattern %q", name, input.Validation.Pattern, Pattern)
				}
				if !idRE.MatchString(input.Default) {
					t.Errorf("input %q default %q does not match the shared pattern", name, input.Default)
				}
			}
			for name := range inputs {
				if _, ok := c.inputs[name]; !ok {
					t.Errorf("unexpected model input %q", name)
				}
			}

			agents := make(map[string]bool)
			for name, state := range workflow.States.Steps {
				if state.Type != "agent" {
					continue
				}
				agents[name] = true
				wantModel, ok := c.agents[name]
				if !ok {
					t.Errorf("unexpected agent state %q", name)
					continue
				}
				if state.Provider != "opencode" {
					t.Errorf("agent state %q provider = %q, want opencode", name, state.Provider)
				}
				if state.Options.Model != wantModel {
					t.Errorf("agent state %q model = %q, want %q", name, state.Options.Model, wantModel)
				}
			}
			for name := range c.agents {
				if !agents[name] {
					t.Errorf("missing agent state %q", name)
				}
			}
		})
	}
}
