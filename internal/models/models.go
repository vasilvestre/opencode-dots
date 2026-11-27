// Package models reads, validates and rewrites the model defaults declared by
// the workflow files. It also resolves global presets and per-role overrides.
// Only the default scalar of each model input is replaced; every other byte of
// the file is preserved.
package models

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// Pattern is the identifier shape every workflow input must declare.
const (
	Pattern = `^[A-Za-z0-9][A-Za-z0-9._+-]*/[A-Za-z0-9][A-Za-z0-9._:+/@-]*(#[A-Za-z0-9][A-Za-z0-9._:+-]*)?$`
	// ClaudePattern is the model identifier of the claude CLI: an alias or a
	// full name, no provider prefix, an optional [1m] style suffix.
	ClaudePattern  = `^[A-Za-z0-9][A-Za-z0-9._:+-]*(\[[A-Za-z0-9]+\])?$`
	maxIDLength    = 200
	catalogTimeout = 2 * time.Second
)

var (
	idRE       = regexp.MustCompile(Pattern)
	claudeIDRE = regexp.MustCompile(ClaudePattern)
)

type inputRole struct {
	workflow string
	input    string
	// state is the agent step that reads the model; empty when a pick step
	// reads this input on the agent's behalf.
	state  string
	preset string
	// provider is the agent CLI the model is for: opencode or claude.
	provider string
	// pick names the shell step whose output the agent step reads, for the
	// roles that exist in both providers. claudeInput is the claude twin.
	pick        string
	claudeInput string
}

func (r inputRole) pattern() string {
	if r.provider == "claude" {
		return ClaudePattern
	}
	return Pattern
}

var roles = []inputRole{
	{workflow: "ask-pr", input: "model", state: "ask", preset: "light", provider: "opencode"},
	{workflow: "fix-ci", input: "model", state: "fix", preset: "light", provider: "opencode"},
	{workflow: "_fix-issue-owned", input: "model", state: "fix", preset: "default", provider: "opencode"},
	{workflow: "_fix-issue-external", input: "model", state: "fix", preset: "default", provider: "opencode"},
	{workflow: "reply-pr", input: "model", state: "reply", preset: "light", provider: "opencode"},
	{workflow: "review-pr", input: "review_model", state: "review", preset: "default", provider: "opencode", pick: "pick_review_model", claudeInput: "claude_review_model"},
	{workflow: "review-pr", input: "address_model", state: "address", preset: "light", provider: "opencode", pick: "pick_address_model", claudeInput: "claude_address_model"},
	{workflow: "review-pr", input: "claude_review_model", preset: "claude_default", provider: "claude"},
	{workflow: "review-pr", input: "claude_address_model", preset: "claude_light", provider: "claude"},
	{workflow: "tend-repo", input: "model", state: "learn", preset: "default", provider: "opencode"},
}

func roleFor(key string) (inputRole, bool) {
	for _, role := range roles {
		if role.key() == key {
			return role, true
		}
	}
	return inputRole{}, false
}

// Variable is one workflow model input and its saved default.
type Variable struct {
	Key         string  `json:"key"`
	Workflow    string  `json:"workflow"`
	Input       string  `json:"input"`
	Provider    string  `json:"provider"`
	Value       string  `json:"value"`
	Description string  `json:"description"`
	Preset      string  `json:"preset"`
	Override    *string `json:"override"`
	Conflict    string  `json:"conflict"`

	defaultNode *yaml.Node
	span        scalarSpan
	file        *workflowFile
}

type scalarSpan struct {
	start int
	end   int
}

type workflowFile struct {
	path     string
	abs      string
	original []byte
}

// Snapshot is the state of every workflow file at one read.
type Snapshot struct {
	files     []*workflowFile
	variables []*Variable
	problems  []string

	configuration  *Configuration
	presetPath     string
	presetAbs      string
	presetOriginal []byte
	migration      bool
}

type replacement struct {
	span  scalarSpan
	token string
}

type fileChange struct {
	path     string
	abs      string
	backup   string
	original []byte
	data     []byte
	created  bool
}

// Saved names a rewritten file and its backup. Backup is empty for a new file.
type Saved struct {
	Path   string `json:"path"`
	Backup string `json:"backup"`
}

// LoadCatalog asks the local opencode for its model IDs; any failure yields an
// empty list.
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
	for _, line := range strings.Split(string(data), "\n") {
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

// Read inspects the named workflow files. resolve maps a root-relative path to
// an absolute one, or refuses it.
func Read(names []string, resolve func(rel string) (string, bool)) (*Snapshot, error) {
	snapshot := &Snapshot{variables: []*Variable{}}
	seen := map[string]bool{}
	for _, name := range names {
		workflow := strings.TrimSuffix(name, ".yaml")
		seen[workflow] = true
		rel := "workflows/" + name
		abs, ok := resolve(rel)
		if !ok {
			snapshot.problems = append(snapshot.problems, "refused workflow path "+rel)
			continue
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", rel, err)
		}
		file := &workflowFile{path: rel, abs: abs, original: data}
		snapshot.files = append(snapshot.files, file)
		variables, problems := inspect(rel, data)
		for _, variable := range variables {
			variable.file = file
			snapshot.variables = append(snapshot.variables, variable)
		}
		if len(problems) == 0 {
			continue
		}
		if len(variables) == 0 {
			for _, problem := range problems {
				snapshot.problems = append(snapshot.problems, rel+": "+problem)
			}
			continue
		}
		for _, variable := range variables {
			for _, problem := range problems {
				addConflict(variable, problem)
			}
		}
	}
	reported := map[string]bool{}
	for _, role := range roles {
		if seen[role.workflow] || reported[role.workflow] {
			continue
		}
		snapshot.problems = append(snapshot.problems, "missing workflow "+role.workflow+".yaml")
		reported[role.workflow] = true
	}
	return snapshot, nil
}

func inspect(rel string, data []byte) ([]*Variable, []string) {
	workflow := strings.TrimSuffix(filepath.Base(rel), ".yaml")
	var problems []string
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		return nil, []string{"invalid YAML: " + err.Error()}
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, []string{"workflow must contain one YAML document"}
		}
		return nil, []string{"invalid YAML: " + err.Error()}
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return nil, []string{"workflow must be a YAML mapping"}
	}
	root, err := nodeFields(document.Content[0])
	if err != nil {
		return nil, []string{err.Error()}
	}
	if scalarString(root["name"]) != workflow {
		problems = append(problems, fmt.Sprintf("workflow name must match filename stem %q", workflow))
	}
	inputs, ok := root["inputs"]
	if !ok || inputs.Tag == "!!null" {
		inputs = &yaml.Node{Kind: yaml.SequenceNode}
	} else if inputs.Kind != yaml.SequenceNode {
		return nil, []string{"inputs must be a sequence"}
	}

	stateModels, stateProblems := agentModels(root["states"])
	problems = append(problems, stateProblems...)
	type candidate struct {
		variable *Variable
		fields   map[string]*yaml.Node
	}
	var candidates []candidate
	counts := map[string]int{}
	for i, input := range inputs.Content {
		fields, err := nodeFields(input)
		if err != nil {
			problems = append(problems, fmt.Sprintf("input %d: %v", i+1, err))
			continue
		}
		name := scalarString(fields["name"])
		if name == "" {
			problems = append(problems, fmt.Sprintf("input %d has no string name", i+1))
			continue
		}
		if name != "model" && !strings.HasSuffix(name, "_model") {
			continue
		}
		variable := &Variable{
			Key:         workflow + "/" + name,
			Workflow:    workflow,
			Input:       name,
			Description: scalarString(fields["description"]),
		}
		candidates = append(candidates, candidate{variable: variable, fields: fields})
		counts[name]++
	}

	expectedInputs := map[string]bool{}
	expectedStates := map[string]string{}
	for _, role := range roles {
		if role.workflow != workflow {
			continue
		}
		expectedInputs[role.input] = true
		if role.state != "" {
			expectedStates[role.state] = role.input
			want := "{{.inputs." + role.input + "}}"
			if role.pick != "" {
				want = "{{.states." + role.pick + ".Output}}"
				problems = append(problems, pickProblems(root["states"], role)...)
			}
			got, ok := stateModels[role.state]
			if !ok {
				problems = append(problems, fmt.Sprintf("missing agent state %q for model input %q", role.state, role.input))
			} else if got != want {
				problems = append(problems, fmt.Sprintf("agent state %q must use %q in options.model", role.state, want))
			}
		}
		if counts[role.input] == 0 {
			problems = append(problems, fmt.Sprintf("missing model input %q", role.input))
		}
	}
	for state := range stateModels {
		if _, ok := expectedStates[state]; !ok {
			problems = append(problems, fmt.Sprintf("unexpected agent state %q", state))
		}
	}

	variables := make([]*Variable, 0, len(candidates))
	for _, item := range candidates {
		variable := item.variable
		fields := item.fields
		pattern := Pattern
		if role, ok := roleFor(variable.Key); ok {
			variable.Provider = role.provider
			pattern = role.pattern()
		} else {
			addConflict(variable, fmt.Sprintf("unexpected model input %q for workflow %q", variable.Input, workflow))
		}
		if counts[variable.Input] > 1 {
			addConflict(variable, "duplicate input name")
		}
		if scalarString(fields["type"]) != "string" {
			addConflict(variable, "type must be string")
		}
		required := fields["required"]
		if required == nil || required.Kind != yaml.ScalarNode || required.Tag != "!!bool" || !strings.EqualFold(required.Value, "false") {
			addConflict(variable, "required must be false")
		}
		if def := fields["default"]; def != nil {
			if def.Kind == yaml.ScalarNode {
				variable.Value = def.Value
				if strings.TrimSpace(variable.Value) == "" {
					addConflict(variable, "default must be a non-empty model identifier")
				} else if !modelRE(pattern).MatchString(variable.Value) {
					addConflict(variable, "default is not a valid model identifier")
				}
			}
			span, err := defaultSpan(data, def)
			if err != nil {
				addConflict(variable, err.Error())
			} else {
				variable.defaultNode = def
				variable.span = span
			}
		} else {
			addConflict(variable, "missing default")
		}
		got := ""
		if validation, ok := fields["validation"]; ok {
			validationFields, err := nodeFields(validation)
			if err != nil {
				addConflict(variable, "invalid validation: "+err.Error())
			} else {
				got = scalarString(validationFields["pattern"])
			}
		}
		if got != pattern {
			addConflict(variable, "validation pattern must match the model pattern of its provider")
		}
		variables = append(variables, variable)
	}
	return variables, problems
}

func nodeFields(node *yaml.Node) (map[string]*yaml.Node, error) {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("expected a mapping")
	}
	fields := make(map[string]*yaml.Node, len(node.Content)/2)
	for i := 0; i < len(node.Content); i += 2 {
		key, value := node.Content[i], node.Content[i+1]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
			return nil, fmt.Errorf("mapping key %d is not a string", i/2+1)
		}
		if _, exists := fields[key.Value]; exists {
			return nil, fmt.Errorf("duplicate field %q", key.Value)
		}
		fields[key.Value] = value
	}
	return fields, nil
}

func scalarString(node *yaml.Node) string {
	if node == nil || node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
		return ""
	}
	return node.Value
}

func agentModels(states *yaml.Node) (map[string]string, []string) {
	models := map[string]string{}
	if states == nil || states.Tag == "!!null" {
		return models, nil
	}
	fields, err := nodeFields(states)
	if err != nil {
		return models, []string{"invalid states: " + err.Error()}
	}
	names := make([]string, 0, len(fields))
	for name := range fields {
		if name != "initial" {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	var problems []string
	for _, name := range names {
		state, err := nodeFields(fields[name])
		if err != nil {
			problems = append(problems, "state "+name+": "+err.Error())
			continue
		}
		if scalarString(state["type"]) != "agent" {
			continue
		}
		models[name] = ""
		options, ok := state["options"]
		if !ok || options == nil || options.Tag == "!!null" {
			continue
		}
		optionFields, err := nodeFields(options)
		if err != nil {
			problems = append(problems, "state "+name+": invalid options: "+err.Error())
			continue
		}
		models[name] = scalarString(optionFields["model"])
	}
	return models, problems
}

func modelRE(pattern string) *regexp.Regexp {
	if pattern == ClaudePattern {
		return claudeIDRE
	}
	return idRE
}

// pickProblems checks the shell step that chooses between the opencode and the
// claude model of one role: it must exist and read both inputs and the provider.
func pickProblems(states *yaml.Node, role inputRole) []string {
	fields, err := nodeFields(states)
	if err != nil {
		return nil
	}
	node, ok := fields[role.pick]
	if !ok {
		return []string{fmt.Sprintf("missing step %q that picks the model of %q", role.pick, role.input)}
	}
	step, err := nodeFields(node)
	if err != nil {
		return []string{"step " + role.pick + ": " + err.Error()}
	}
	command := scalarString(step["command"])
	var problems []string
	for _, ref := range []string{role.input, role.claudeInput, "provider"} {
		if !strings.Contains(command, "{{.inputs."+ref+"}}") {
			problems = append(problems, fmt.Sprintf("step %q must read {{.inputs.%s}}", role.pick, ref))
		}
	}
	return problems
}

func addConflict(variable *Variable, problem string) {
	if variable.Conflict == "" {
		variable.Conflict = problem
		return
	}
	variable.Conflict += "; " + problem
}

// Variables lists every model input found, in file order.
func (s *Snapshot) Variables() []*Variable { return s.variables }

// Problems lists structural faults that stop any edit.
func (s *Snapshot) Problems() []string { return s.problems }

func (s *Snapshot) conflicts() []string {
	var problems []string
	problems = append(problems, s.problems...)
	for _, variable := range s.variables {
		if variable.Conflict != "" {
			problems = append(problems, variable.Key+": "+variable.Conflict)
		}
	}
	return problems
}

// Prepare checks values against the discovered variables and builds the new
// file contents without touching the disk.
func (s *Snapshot) Prepare(values map[string]string) (*Plan, error) {
	if problems := s.conflicts(); len(problems) > 0 {
		return nil, errors.New(strings.Join(problems, "; "))
	}
	current := make(map[string]bool, len(s.variables))
	for _, variable := range s.variables {
		current[variable.Key] = true
	}
	var unknown, missing []string
	for key := range values {
		if !current[key] {
			unknown = append(unknown, key)
		}
	}
	for _, variable := range s.variables {
		if _, ok := values[variable.Key]; !ok {
			missing = append(missing, variable.Key)
		}
	}
	slices.Sort(unknown)
	slices.Sort(missing)
	if len(unknown) > 0 || len(missing) > 0 {
		return nil, fmt.Errorf("variables must match the discovered keys (unknown: %s; missing: %s)", strings.Join(unknown, ", "), strings.Join(missing, ", "))
	}

	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		if err := validateRoleModel(key, values[key]); err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
	}
	if err := distinctReviewModels(func(key string) string { return values[key] }); err != nil {
		return nil, err
	}

	return s.prepareWorkflowChanges(values)
}

func validateModelID(value string) error { return validateModel(value, Pattern) }

// distinctReviewModels keeps the reviewer from approving its own output, for
// each provider.
func distinctReviewModels(get func(key string) string) error {
	if get("review-pr/review_model") == get("review-pr/address_model") {
		return errors.New("review-pr review_model and address_model must be different")
	}
	if get("review-pr/claude_review_model") == get("review-pr/claude_address_model") {
		return errors.New("review-pr claude_review_model and claude_address_model must be different")
	}
	return nil
}

func validateModel(value, pattern string) error {
	if len(value) > maxIDLength {
		return fmt.Errorf("model identifier is longer than %d characters", maxIDLength)
	}
	if !modelRE(pattern).MatchString(value) {
		return errors.New("model identifier is not valid")
	}
	return nil
}

func validateRoleModel(key, value string) error {
	role, ok := roleFor(key)
	if !ok {
		return validateModelID(value)
	}
	return validateModel(value, role.pattern())
}

func (s *Snapshot) prepareWorkflowChanges(values map[string]string) (*Plan, error) {
	byFile := map[*workflowFile][]replacement{}
	for _, variable := range s.variables {
		value := values[variable.Key]
		if value == variable.Value {
			continue
		}
		token, err := scalarReplacement(variable.defaultNode, value)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", variable.Key, err)
		}
		byFile[variable.file] = append(byFile[variable.file], replacement{span: variable.span, token: token})
	}

	changes := []fileChange{}
	for _, file := range s.files {
		replacements := byFile[file]
		if len(replacements) == 0 {
			continue
		}
		sort.Slice(replacements, func(i, j int) bool { return replacements[i].span.start > replacements[j].span.start })
		data := append([]byte(nil), file.original...)
		for _, replacement := range replacements {
			out := make([]byte, 0, len(data)+len(replacement.token)-replacement.span.end+replacement.span.start)
			out = append(out, data[:replacement.span.start]...)
			out = append(out, replacement.token...)
			out = append(out, data[replacement.span.end:]...)
			data = out
		}
		var probe any
		if err := yaml.Unmarshal(data, &probe); err != nil {
			return nil, fmt.Errorf("%s would not parse after the update: %w", file.path, err)
		}
		changes = append(changes, fileChange{path: file.path, abs: file.abs, original: file.original, data: data})
	}
	return &Plan{changes: changes}, nil
}

func scalarReplacement(node *yaml.Node, value string) (string, error) {
	switch node.Style {
	case 0:
		return value, nil
	case yaml.SingleQuotedStyle:
		return "'" + strings.ReplaceAll(value, "'", "''") + "'", nil
	case yaml.DoubleQuotedStyle:
		return strconv.Quote(value), nil
	default:
		return "", fmt.Errorf("unsupported default scalar style")
	}
}

func defaultSpan(data []byte, node *yaml.Node) (scalarSpan, error) {
	if node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
		return scalarSpan{}, fmt.Errorf("default must be a string scalar")
	}
	switch node.Style {
	case 0, yaml.SingleQuotedStyle, yaml.DoubleQuotedStyle:
	default:
		return scalarSpan{}, fmt.Errorf("unsupported default scalar style")
	}
	if node.Anchor != "" || node.Alias != nil {
		return scalarSpan{}, fmt.Errorf("unsupported default scalar style")
	}
	if strings.ContainsAny(node.Value, "\r\n") {
		return scalarSpan{}, fmt.Errorf("default must be a one-line scalar")
	}
	start, err := yamlNodeOffset(data, node)
	if err != nil {
		return scalarSpan{}, err
	}
	lineEnd := start
	for lineEnd < len(data) && data[lineEnd] != '\r' && data[lineEnd] != '\n' {
		lineEnd++
	}
	end := 0
	switch node.Style {
	case 0:
		end = start
		for end < lineEnd {
			if data[end] == ',' || data[end] == ']' || data[end] == '}' {
				break
			}
			if data[end] == '#' && (end == start || data[end-1] == ' ' || data[end-1] == '\t') {
				break
			}
			end++
		}
		for end > start && (data[end-1] == ' ' || data[end-1] == '\t') {
			end--
		}
	case yaml.SingleQuotedStyle:
		if start >= lineEnd || data[start] != '\'' {
			return scalarSpan{}, fmt.Errorf("default quote does not match its YAML node")
		}
		for i := start + 1; i < lineEnd; i++ {
			if data[i] != '\'' {
				continue
			}
			if i+1 < lineEnd && data[i+1] == '\'' {
				i++
				continue
			}
			end = i + 1
			break
		}
	case yaml.DoubleQuotedStyle:
		if start >= lineEnd || data[start] != '"' {
			return scalarSpan{}, fmt.Errorf("default quote does not match its YAML node")
		}
		escaped := false
		for i := start + 1; i < lineEnd; i++ {
			if escaped {
				escaped = false
				continue
			}
			if data[i] == '\\' {
				escaped = true
				continue
			}
			if data[i] == '"' {
				end = i + 1
				break
			}
		}
	}
	if end <= start {
		return scalarSpan{}, fmt.Errorf("default scalar token is missing or spans multiple lines")
	}
	return scalarSpan{start: start, end: end}, nil
}

func yamlNodeOffset(data []byte, node *yaml.Node) (int, error) {
	if node.Line < 1 || node.Column < 1 {
		return 0, fmt.Errorf("default scalar has no YAML position")
	}
	line, start := 1, 0
	for line < node.Line {
		i := start
		for i < len(data) && data[i] != '\r' && data[i] != '\n' {
			i++
		}
		if i == len(data) {
			return 0, fmt.Errorf("default scalar line is outside the file")
		}
		if data[i] == '\r' && i+1 < len(data) && data[i+1] == '\n' {
			i++
		}
		start = i + 1
		line++
	}
	offset := start
	for column := 1; column < node.Column; column++ {
		if offset >= len(data) || data[offset] == '\r' || data[offset] == '\n' {
			return 0, fmt.Errorf("default scalar column is outside the line")
		}
		_, size := utf8.DecodeRune(data[offset:])
		offset += size
	}
	if offset >= len(data) || data[offset] == '\r' || data[offset] == '\n' {
		return 0, fmt.Errorf("default scalar position is empty")
	}
	return offset, nil
}

// Plan is a prepared set of file rewrites.
type Plan struct {
	changes []fileChange
}

// Apply backs up each existing file with backup, then writes it with write.
// When a write fails, written files are restored and new files are removed.
func (p *Plan) Apply(backup func(abs string) (string, error), write func(abs string, data []byte) error) ([]Saved, error) {
	for i := range p.changes {
		if p.changes[i].created {
			continue
		}
		path, err := backup(p.changes[i].abs)
		if err != nil {
			return nil, fmt.Errorf("backup failed: %w", err)
		}
		if path == "" {
			return nil, fmt.Errorf("%s disappeared before backup", p.changes[i].path)
		}
		p.changes[i].backup = path
	}
	if err := writeAll(p.changes, write); err != nil {
		return nil, err
	}
	saved := make([]Saved, 0, len(p.changes))
	for _, change := range p.changes {
		saved = append(saved, Saved{Path: change.path, Backup: change.backup})
	}
	return saved, nil
}

func restoreFile(change fileChange, write func(string, []byte) error) error {
	if !change.created {
		return write(change.abs, change.original)
	}
	if err := os.Remove(change.abs); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func writeAll(changes []fileChange, write func(string, []byte) error) error {
	for i, change := range changes {
		if err := write(change.abs, change.data); err != nil {
			problems := []error{fmt.Errorf("write %s: %w", change.path, err)}
			for j := i; j >= 0; j-- {
				if err := restoreFile(changes[j], write); err != nil {
					problems = append(problems, fmt.Errorf("restore %s: %w", changes[j].path, err))
				}
			}
			return errors.Join(problems...)
		}
	}
	return nil
}
