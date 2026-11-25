package models

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	presetVersion = 1
	presetsFile   = "model-presets.yaml"
)

// Every preset the file may hold. light and default are required. The claude
// pair is optional so a file written before per-provider presets stays valid;
// a missing one takes the value in claudeDefaults.
var (
	presetNames     = []string{"default", "light", "claude_default", "claude_light"}
	requiredPresets = []string{"default", "light"}
	claudeDefaults  = map[string]string{"claude_default": "sonnet", "claude_light": "haiku"}
)

func presetPattern(name string) string {
	if strings.HasPrefix(name, "claude_") {
		return ClaudePattern
	}
	return Pattern
}

func (c *Configuration) fillDefaults() {
	if c.Presets == nil {
		c.Presets = map[string]string{}
	}
	for name, value := range claudeDefaults {
		if _, ok := c.Presets[name]; !ok {
			c.Presets[name] = value
		}
	}
}

// Configuration is the versioned content of model-presets.yaml.
type Configuration struct {
	Version   int                `yaml:"version" json:"version"`
	Presets   map[string]string  `yaml:"presets" json:"presets"`
	Overrides map[string]*string `yaml:"overrides,omitempty" json:"overrides"`
}

// Update changes preset IDs and selected role overrides. A nil override removes
// the override. Missing override keys keep their current values.
type Update struct {
	Presets   map[string]string  `yaml:"presets" json:"presets"`
	Overrides map[string]*string `yaml:"overrides" json:"overrides"`
}

// ReadConfigured reads workflows and the fixed root preset file. A missing
// preset file produces an in-memory migration without writing anything.
func ReadConfigured(names []string, resolve func(rel string) (string, bool), presetsAbs string) (*Snapshot, error) {
	snapshot, err := Read(names, resolve)
	if err != nil {
		return nil, err
	}
	if presetsAbs == "" {
		return nil, errors.New("model preset path is empty")
	}
	snapshot.presetPath = presetsFile
	snapshot.presetAbs = presetsAbs
	workflowProblems := snapshot.conflicts()

	data, err := os.ReadFile(presetsAbs)
	if err == nil {
		configuration, err := parseConfiguration(data)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", presetsFile, err)
		}
		snapshot.configuration = configuration
		snapshot.presetOriginal = data
	} else if os.IsNotExist(err) {
		if len(workflowProblems) > 0 {
			return snapshot, nil
		}
		configuration, err := migrateConfiguration(snapshot)
		if err != nil {
			return nil, err
		}
		snapshot.configuration = configuration
		snapshot.migration = true
	} else {
		return nil, fmt.Errorf("read %s: %w", presetsFile, err)
	}

	snapshot.applyConfiguration()
	return snapshot, nil
}

// Configuration returns a copy of the resolved preset configuration.
func (s *Snapshot) Configuration() Configuration {
	if s.configuration == nil {
		return Configuration{}
	}
	return s.configuration.clone()
}

// Presets returns a copy of the two global preset IDs.
func (s *Snapshot) Presets() map[string]string {
	if s.configuration == nil {
		return map[string]string{}
	}
	return cloneStrings(s.configuration.Presets)
}

// MigrationRequired reports whether the preset file still needs its first save.
func (s *Snapshot) MigrationRequired() bool { return s.migration }

// PrepareUpdate builds workflow replacements and the preset-file write as one
// plan. Workflow changes stay first so rollback can remove a new preset file.
func (s *Snapshot) PrepareUpdate(update Update) (*Plan, error) {
	if problems := s.conflicts(); len(problems) > 0 {
		return nil, errors.New(strings.Join(problems, "; "))
	}
	if s.configuration == nil {
		return nil, errors.New("model preset configuration is not loaded")
	}
	if err := validatePresetKeys(update.Presets); err != nil {
		return nil, err
	}

	next := s.configuration.clone()
	for _, name := range presetNames {
		value, ok := update.Presets[name]
		if !ok {
			continue // an optional preset the request omits keeps its value
		}
		if err := validateModel(value, presetPattern(name)); err != nil {
			return nil, fmt.Errorf("preset %q: %w", name, err)
		}
		next.Presets[name] = value
	}
	keys := make([]string, 0, len(update.Overrides))
	for key := range update.Overrides {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if !isRole(key) {
			return nil, fmt.Errorf("unknown model role %q", key)
		}
		value := update.Overrides[key]
		if value == nil {
			delete(next.Overrides, key)
			continue
		}
		if err := validateRoleModel(key, *value); err != nil {
			return nil, fmt.Errorf("override %q: %w", key, err)
		}
		copied := *value
		next.Overrides[key] = &copied
	}
	if err := next.validate(); err != nil {
		return nil, err
	}

	values := make(map[string]string, len(roles))
	for _, role := range roles {
		values[role.key()] = next.resolve(role.key())
	}
	plan, err := s.prepareWorkflowChanges(values)
	if err != nil {
		return nil, err
	}
	if !s.migration && configurationsEqual(s.configuration, &next) {
		return plan, nil
	}
	data, err := next.marshal()
	if err != nil {
		return nil, fmt.Errorf("serialize %s: %w", presetsFile, err)
	}
	plan.changes = append(plan.changes, fileChange{
		path:     presetsFile,
		abs:      s.presetAbs,
		original: s.presetOriginal,
		data:     data,
		created:  s.migration,
	})
	return plan, nil
}

func parseConfiguration(data []byte) (*Configuration, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var configuration Configuration
	if err := decoder.Decode(&configuration); err != nil {
		if err == io.EOF {
			return nil, errors.New("preset file is empty")
		}
		return nil, err
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, errors.New("preset file must contain one YAML document")
		}
		return nil, err
	}
	configuration.fillDefaults()
	if err := configuration.validate(); err != nil {
		return nil, err
	}
	for key, value := range configuration.Overrides {
		if value == nil {
			delete(configuration.Overrides, key)
		}
	}
	if configuration.Overrides == nil {
		configuration.Overrides = map[string]*string{}
	}
	return &configuration, nil
}

func (c *Configuration) validate() error {
	if c.Version != presetVersion {
		return fmt.Errorf("unsupported preset version %d", c.Version)
	}
	if err := validatePresetKeys(c.Presets); err != nil {
		return err
	}
	for _, name := range presetNames {
		if err := validateModel(c.Presets[name], presetPattern(name)); err != nil {
			return fmt.Errorf("preset %q: %w", name, err)
		}
	}
	keys := make([]string, 0, len(c.Overrides))
	for key := range c.Overrides {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if !isRole(key) {
			return fmt.Errorf("unknown model role %q", key)
		}
		if value := c.Overrides[key]; value != nil {
			if err := validateRoleModel(key, *value); err != nil {
				return fmt.Errorf("override %q: %w", key, err)
			}
		}
	}
	return distinctReviewModels(c.resolve)
}

func validatePresetKeys(presets map[string]string) error {
	known := map[string]bool{}
	for _, name := range presetNames {
		known[name] = true
	}
	var unknown, missing []string
	for key := range presets {
		if !known[key] {
			unknown = append(unknown, key)
		}
	}
	for _, name := range requiredPresets {
		if _, ok := presets[name]; !ok {
			missing = append(missing, name)
		}
	}
	sort.Strings(unknown)
	sort.Strings(missing)
	if len(unknown) > 0 || len(missing) > 0 {
		return fmt.Errorf("presets must contain light and default, and may add claude_light and claude_default (unknown: %s; missing: %s)", strings.Join(unknown, ", "), strings.Join(missing, ", "))
	}
	return nil
}

func (c *Configuration) resolve(key string) string {
	for _, role := range roles {
		if role.key() != key {
			continue
		}
		if value := c.Overrides[key]; value != nil {
			return *value
		}
		return c.Presets[role.preset]
	}
	return ""
}

func (c *Configuration) clone() Configuration {
	copied := Configuration{
		Version:   c.Version,
		Presets:   cloneStrings(c.Presets),
		Overrides: make(map[string]*string, len(c.Overrides)),
	}
	for key, value := range c.Overrides {
		if value != nil {
			item := *value
			copied.Overrides[key] = &item
		}
	}
	return copied
}

func (c *Configuration) marshal() ([]byte, error) {
	document := struct {
		Version int `yaml:"version"`
		Presets struct {
			Light         string `yaml:"light"`
			Default       string `yaml:"default"`
			ClaudeLight   string `yaml:"claude_light,omitempty"`
			ClaudeDefault string `yaml:"claude_default,omitempty"`
		} `yaml:"presets"`
		Overrides map[string]*string `yaml:"overrides,omitempty"`
	}{
		Version:   c.Version,
		Overrides: c.Overrides,
	}
	document.Presets.Light = c.Presets["light"]
	document.Presets.Default = c.Presets["default"]
	document.Presets.ClaudeLight = c.Presets["claude_light"]
	document.Presets.ClaudeDefault = c.Presets["claude_default"]
	return yaml.Marshal(document)
}

func configurationsEqual(a, b *Configuration) bool {
	if a.Version != b.Version || len(a.Presets) != len(b.Presets) || len(a.Overrides) != len(b.Overrides) {
		return false
	}
	for key, value := range a.Presets {
		if b.Presets[key] != value {
			return false
		}
	}
	for key, value := range a.Overrides {
		other, ok := b.Overrides[key]
		if !ok || (value == nil) != (other == nil) {
			return false
		}
		if value != nil && *value != *other {
			return false
		}
	}
	return true
}

func cloneStrings(values map[string]string) map[string]string {
	copied := make(map[string]string, len(values))
	for key, value := range values {
		copied[key] = value
	}
	return copied
}

func migrateConfiguration(snapshot *Snapshot) (*Configuration, error) {
	values := map[string]string{}
	for _, variable := range snapshot.variables {
		values[variable.Key] = variable.Value
	}
	configuration := &Configuration{
		Version: presetVersion,
		Presets: map[string]string{
			"light":   values["ask-pr/model"],
			"default": values["_fix-issue-owned/model"],
		},
		Overrides: map[string]*string{},
	}
	if value := values["review-pr/claude_review_model"]; value != "" {
		configuration.Presets["claude_default"] = value
	}
	if value := values["review-pr/claude_address_model"]; value != "" {
		configuration.Presets["claude_light"] = value
	}
	configuration.fillDefaults()
	for _, role := range roles {
		value := values[role.key()]
		if value == "" {
			continue // a workflow that predates this input has nothing to keep
		}
		if value != configuration.Presets[role.preset] {
			copied := value
			configuration.Overrides[role.key()] = &copied
		}
	}
	if err := configuration.validate(); err != nil {
		return nil, fmt.Errorf("cannot migrate model presets: %w", err)
	}
	return configuration, nil
}

func (s *Snapshot) applyConfiguration() {
	byKey := map[string]*Variable{}
	for _, variable := range s.variables {
		byKey[variable.Key] = variable
	}
	for _, role := range roles {
		variable := byKey[role.key()]
		if variable == nil {
			continue
		}
		variable.Preset = role.preset
		if value := s.configuration.Overrides[role.key()]; value != nil {
			copied := *value
			variable.Override = &copied
		}
		resolved := s.configuration.resolve(role.key())
		if variable.Value != resolved {
			addConflict(variable, fmt.Sprintf("workflow default %q differs from resolved model %q", variable.Value, resolved))
		}
	}
}

func isRole(key string) bool {
	for _, role := range roles {
		if role.key() == key {
			return true
		}
	}
	return false
}

func (r inputRole) key() string { return r.workflow + "/" + r.input }
