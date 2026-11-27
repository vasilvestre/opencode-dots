// Package models holds the model presets (ADR 0052): the model-presets.yaml file, the roles
// that read it, the validation of a model identifier and the catalog of the local opencode.
// Workflows read the file when a run starts (bin/model), so nothing here rewrites a workflow.
package models

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	// File is the presets file at the root of the repository.
	File = "model-presets.yaml"
	// Pattern is the identifier shape of an opencode model: provider/model#variant.
	Pattern = `^[A-Za-z0-9][A-Za-z0-9._+-]*/[A-Za-z0-9][A-Za-z0-9._:+/@-]*(#[A-Za-z0-9][A-Za-z0-9._:+-]*)?$`
	// ClaudePattern is the identifier of the claude CLI: an alias or a full name, no provider
	// prefix, an optional [1m] style suffix.
	ClaudePattern  = `^[A-Za-z0-9][A-Za-z0-9._:+-]*(\[[A-Za-z0-9]+\])?$`
	version        = 1
	maxIDLength    = 200
	catalogTimeout = 2 * time.Second
)

var (
	idRE       = regexp.MustCompile(Pattern)
	claudeIDRE = regexp.MustCompile(ClaudePattern)
	// presetNames are the four presets the file holds, all required.
	presetNames = []string{"claude_default", "claude_light", "default", "light"}
)

// Role is one model a workflow reads: the Input of Workflow, assigned to a preset. The workflow
// asks for it with `bin/model KEY PRESET`; a test keeps this table and the workflows in step.
type Role struct {
	Key      string `json:"key"`
	Workflow string `json:"workflow"`
	Input    string `json:"input"`
	Provider string `json:"provider"` // the agent CLI the model is for: opencode or claude
	Preset   string `json:"preset"`
}

func role(workflow, input, preset, provider string) Role {
	return Role{Key: workflow + "/" + input, Workflow: workflow, Input: input, Provider: provider, Preset: preset}
}

// Roles is the fixed table of model roles.
var Roles = []Role{
	role("ask-pr", "model", "light", "opencode"),
	role("fix-ci", "model", "light", "opencode"),
	role("_fix-issue-owned", "model", "default", "opencode"),
	role("_fix-issue-external", "model", "default", "opencode"),
	role("reply-pr", "model", "light", "opencode"),
	role("review-pr", "review_model", "default", "opencode"),
	role("review-pr", "address_model", "light", "opencode"),
	role("review-pr", "claude_review_model", "claude_default", "claude"),
	role("review-pr", "claude_address_model", "claude_light", "claude"),
	role("tend-repo", "model", "default", "opencode"),
}

func roleFor(key string) (Role, bool) {
	i := slices.IndexFunc(Roles, func(r Role) bool { return r.Key == key })
	if i < 0 {
		return Role{}, false
	}
	return Roles[i], true
}

// Configuration is the content of model-presets.yaml.
type Configuration struct {
	Version   int               `yaml:"version"`
	Presets   map[string]string `yaml:"presets"`
	Overrides map[string]string `yaml:"overrides,omitempty"`
}

// Variable is a role with what it resolves to, as the Config view shows it.
type Variable struct {
	Role
	Value    string  `json:"value"`
	Override *string `json:"override"`
}

// Load reads and validates the presets file.
func Load(path string) (*Configuration, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// Parse reads one YAML document, refuses unknown keys and validates it.
func Parse(data []byte) (*Configuration, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var c Configuration
	if err := dec.Decode(&c); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("preset file is empty")
		}
		return nil, err
	}
	if err := dec.Decode(&yaml.Node{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("preset file must contain one YAML document")
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// Validate checks the version, the four presets, every override and, for each provider, that the
// reviewer is not the model that fixes what it reviewed.
func (c *Configuration) Validate() error {
	if c.Version != version {
		return fmt.Errorf("unsupported preset version %d", c.Version)
	}
	for _, name := range presetNames {
		if err := validateModel(c.Presets[name], patternOf(name)); err != nil {
			return fmt.Errorf("preset %q: %w", name, err)
		}
	}
	for name := range c.Presets {
		if !slices.Contains(presetNames, name) {
			return fmt.Errorf("unknown preset %q: the presets are %s", name, strings.Join(presetNames, ", "))
		}
	}
	for key, value := range c.Overrides {
		r, ok := roleFor(key)
		if !ok {
			return fmt.Errorf("unknown model role %q", key)
		}
		if err := validateModel(value, patternOf(r.Preset)); err != nil {
			return fmt.Errorf("override %q: %w", key, err)
		}
	}
	if c.Resolve("review-pr/review_model") == c.Resolve("review-pr/address_model") {
		return errors.New("review-pr review_model and address_model must be different")
	}
	if c.Resolve("review-pr/claude_review_model") == c.Resolve("review-pr/claude_address_model") {
		return errors.New("review-pr claude_review_model and claude_address_model must be different")
	}
	return nil
}

// Resolve is the model of a role: its override, else the preset it is assigned to.
func (c *Configuration) Resolve(key string) string {
	r, ok := roleFor(key)
	if !ok {
		return ""
	}
	if v, ok := c.Overrides[key]; ok {
		return v
	}
	return c.Presets[r.Preset]
}

// Variables lists every role with its resolved model and its override, if any.
func (c *Configuration) Variables() []Variable {
	out := make([]Variable, 0, len(Roles))
	for _, r := range Roles {
		v := Variable{Role: r, Value: c.Resolve(r.Key)}
		if o, ok := c.Overrides[r.Key]; ok {
			v.Override = &o
		}
		out = append(out, v)
	}
	return out
}

// Updated returns a validated copy with the given presets replaced and the overrides set (a nil
// override clears it). The receiver is left as it was.
func (c *Configuration) Updated(presets map[string]string, overrides map[string]*string) (*Configuration, error) {
	next := &Configuration{Version: c.Version, Presets: maps.Clone(c.Presets), Overrides: maps.Clone(c.Overrides)}
	if next.Overrides == nil {
		next.Overrides = map[string]string{}
	}
	maps.Copy(next.Presets, presets)
	for key, value := range overrides {
		if value == nil {
			delete(next.Overrides, key)
		} else {
			next.Overrides[key] = *value
		}
	}
	if err := next.Validate(); err != nil {
		return nil, err
	}
	return next, nil
}

// Marshal is the file content.
func (c *Configuration) Marshal() ([]byte, error) { return yaml.Marshal(c) }

func patternOf(name string) string {
	if strings.HasPrefix(name, "claude_") {
		return ClaudePattern
	}
	return Pattern
}

func validateModel(value, pattern string) error {
	if len(value) > maxIDLength {
		return fmt.Errorf("model identifier is longer than %d characters", maxIDLength)
	}
	re := idRE
	if pattern == ClaudePattern {
		re = claudeIDRE
	}
	if !re.MatchString(value) {
		return errors.New("model identifier is not valid")
	}
	return nil
}

// LoadCatalog asks the local opencode for its model IDs; any failure yields an empty list.
func LoadCatalog(ctx context.Context) []string {
	ctx, cancel := context.WithTimeout(ctx, catalogTimeout)
	defer cancel()
	output, err := exec.CommandContext(ctx, "opencode", "models").Output()
	if err != nil {
		return []string{}
	}
	return parseCatalog(output)
}

func parseCatalog(data []byte) []string {
	models := []string{}
	seen := map[string]bool{}
	for line := range strings.SplitSeq(string(data), "\n") {
		model := strings.TrimSpace(line)
		if !idRE.MatchString(model) || seen[model] {
			continue
		}
		seen[model] = true
		models = append(models, model)
	}
	slices.Sort(models)
	return models
}
