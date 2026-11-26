package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"opencode-dots/internal/repos"
)

// The repositories table edits repos.yaml one key at a time. It works on the
// YAML tree, not on a decoded copy, so comments, quoting and key order survive
// a toggle. Every change goes through repos.Build before it reaches the disk,
// exactly like a save from the editor: a refused change leaves the file as it was.

// toggles are the keys the table switches on and off with a checkbox.
var toggles = []string{"review", "reply", "fix_issue", "fix_ci", "tend", "sync"}

var providers = []string{"opencode", "claude"}

var repoNameOK = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

type featureState struct {
	On bool `json:"on"`
	// Source says where the switch comes from: "repo" (the block says it),
	// "profile" (a `use` profile does) or "" when it is off.
	Source string `json:"source,omitempty"`
	// Custom marks a block or a list of rules: a checkbox can switch it off,
	// but not recreate its options, so the page asks before dropping them.
	Custom bool   `json:"custom,omitempty"`
	Detail string `json:"detail,omitempty"`
	// Rules is what the block says, one object per watcher rule, before any
	// default is laid under it. Only the watcher features have it.
	Rules []any `json:"rules,omitempty"`
	// AutoMerge is tend's only option.
	AutoMerge *bool `json:"auto_merge,omitempty"`
}

// watchForm is what the rules dialog needs to know about a watcher feature.
type watchForm struct {
	repos.WatchSchema
	DefaultIgnore []any `json:"default_ignore,omitempty"`
	DefaultOnly   []any `json:"default_only,omitempty"`
}

type repoRow struct {
	Name     string                  `json:"name"`
	Use      []string                `json:"use,omitempty"`
	Features map[string]featureState `json:"features"`
	Provider string                  `json:"provider"`
	// ProviderSource is "repo", "default" or "" (the built-in opencode).
	ProviderSource string `json:"provider_source,omitempty"`
	Checkout       string `json:"checkout,omitempty"`
	// Trust is the level of ADR 0032, empty for the default `open`.
	Trust string `json:"trust,omitempty"`
}

type reposConfig struct {
	Repos     []repoRow            `json:"repos"`
	Profiles  []string             `json:"profiles"`
	Toggles   []string             `json:"toggles"`
	Providers []string             `json:"providers"`
	Forms     map[string]watchForm `json:"forms"`
}

func (d *dash) reposConfigPath() string { return filepath.Join(d.root, repos.SourceFile) }

func (d *dash) reposConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg, err := d.readReposConfig()
		if err != nil {
			writeJSONStatus(w, statusOf(err), map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, cfg)
	case http.MethodPatch:
		d.patchReposConfig(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

type httpError struct {
	status int
	msg    string
}

func (e httpError) Error() string { return e.msg }

func statusOf(err error) int {
	if he, ok := err.(httpError); ok {
		return he.status
	}
	return http.StatusInternalServerError
}

func (d *dash) readReposConfig() (*reposConfig, error) {
	data, err := os.ReadFile(d.reposConfigPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, httpError{http.StatusNotFound, "repos.yaml does not exist yet: copy repos.example.yaml to repos.yaml"}
		}
		return nil, err
	}
	return buildReposConfig(data)
}

func buildReposConfig(data []byte) (*reposConfig, error) {
	doc, err := repos.Parse(data)
	if err != nil {
		return nil, httpError{http.StatusUnprocessableEntity, err.Error()}
	}
	resolved, err := repos.Resolve(doc)
	if err != nil {
		return nil, httpError{http.StatusUnprocessableEntity, err.Error()}
	}
	cfg := &reposConfig{Repos: []repoRow{}, Profiles: []string{}, Toggles: toggles, Providers: providers, Forms: map[string]watchForm{}}
	for _, key := range toggles {
		if schema, ok := repos.WatchSchemaOf(key); ok {
			form := watchForm{WatchSchema: schema}
			if def, ok := doc.Defaults[key].(map[string]any); ok {
				form.DefaultIgnore, _ = def["ignore_authors"].([]any)
				form.DefaultOnly, _ = def["only_authors"].([]any)
			}
			cfg.Forms[key] = form
		}
	}
	for name := range doc.Profiles {
		cfg.Profiles = append(cfg.Profiles, name)
	}
	sort.Strings(cfg.Profiles)
	for i, entry := range doc.Repos {
		res := resolved[i]
		row := repoRow{Name: entry.Name, Features: map[string]featureState{}, Provider: "opencode"}
		for _, p := range useNames(entry.Config["use"]) {
			row.Use = append(row.Use, p)
		}
		for _, key := range toggles {
			st := featureState{}
			if v, on := res.Value(key); on {
				st.On = true
				own, hasOwn := entry.Config[key]
				switch {
				case hasOwn:
					st.Source = "repo"
					st.Custom = isCustom(own)
					st.Detail = flowText(own)
				default:
					st.Source = "profile"
					st.Custom = isCustom(v)
					st.Detail = flowText(v)
				}
				if _, isWatch := cfg.Forms[key]; isWatch {
					st.Rules = ruleList(rawFeature(doc, entry, key))
				}
				if key == "tend" {
					m, _ := v.(map[string]any)
					auto, _ := m["auto_merge"].(bool)
					st.AutoMerge = &auto
				}
			}
			row.Features[key] = st
		}
		if v, _ := res.Value("provider"); v != nil {
			name, _ := v.(string)
			if name != "" {
				row.Provider = name
			}
			if _, own := entry.Config["provider"]; own {
				row.ProviderSource = "repo"
			} else {
				row.ProviderSource = "default"
			}
		}
		if v, _ := res.Value("checkout"); v != nil {
			row.Checkout, _ = v.(string)
		}
		if v, _ := res.Value("trust"); v != nil {
			row.Trust, _ = v.(string)
		}
		cfg.Repos = append(cfg.Repos, row)
	}
	return cfg, nil
}

// rawFeature is what the repository's block says for a key, else what its first
// profile that sets it says: the value before defaults are laid under it.
func rawFeature(doc *repos.Document, entry repos.Entry, key string) any {
	if v, ok := entry.Config[key]; ok {
		return v
	}
	for _, name := range useNames(entry.Config["use"]) {
		if v, ok := doc.Profiles[name][key]; ok {
			return v
		}
	}
	return nil
}

// ruleList normalises a watcher value to one object per rule: true is an empty
// rule, a block is one rule, a list holds one per item.
func ruleList(v any) []any {
	switch x := v.(type) {
	case map[string]any:
		return []any{x}
	case []any:
		out := make([]any, 0, len(x))
		for _, item := range x {
			if m, ok := item.(map[string]any); ok {
				out = append(out, m)
			} else {
				out = append(out, map[string]any{})
			}
		}
		return out
	}
	return []any{map[string]any{}}
}

func useNames(v any) []string {
	switch x := v.(type) {
	case string:
		return []string{x}
	case []any:
		var out []string
		for _, item := range x {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func isCustom(v any) bool {
	switch x := v.(type) {
	case map[string]any:
		return len(x) > 0
	case []any:
		return true
	}
	return false
}

func flowText(v any) string {
	if !isCustom(v) {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

type reposPatch struct {
	Op   string `json:"op"`   // "set" (default), "add", "remove"
	Repo string `json:"repo"` // owner/name
	Key  string `json:"key"`
	// Feature names the watcher feature a "rules" change is for.
	Feature string `json:"feature"`
	Value   any    `json:"value"` // bool for a toggle, string for provider and checkout, null to clear
}

func (d *dash) patchReposConfig(w http.ResponseWriter, r *http.Request) {
	if !d.trustedRequest(r) {
		http.Error(w, "refused origin", http.StatusForbidden)
		return
	}
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		http.Error(w, "expected application/json", http.StatusUnsupportedMediaType)
		return
	}
	var p reposPatch
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&p); err != nil {
		http.Error(w, "bad body: "+err.Error(), http.StatusBadRequest)
		return
	}
	// The repository and the key or operation, never the value.
	auditTarget(r, p.Repo+" "+firstNonEmpty(p.Key, p.Op))
	d.editMu.Lock()
	defer d.editMu.Unlock()

	abs := d.reposConfigPath()
	data, err := os.ReadFile(abs)
	if err != nil {
		msg := "cannot read repos.yaml: " + err.Error()
		status := http.StatusInternalServerError
		if os.IsNotExist(err) {
			msg, status = "repos.yaml does not exist yet: copy repos.example.yaml to repos.yaml", http.StatusNotFound
		}
		writeJSONStatus(w, status, map[string]string{"error": msg})
		return
	}
	next, err := applyReposPatch(data, p)
	if err != nil {
		writeJSONStatus(w, statusOf(err), map[string]string{"error": err.Error()})
		return
	}
	if bytes.Equal(next, data) {
		cfg, err := buildReposConfig(data)
		if err != nil {
			writeJSONStatus(w, statusOf(err), map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, map[string]any{"changed": false, "config": cfg})
		return
	}
	generated, err := repos.Build(next, d.root, repos.Settings{})
	if err != nil {
		writeJSONStatus(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	backup, err := backupFile(abs)
	if err != nil {
		http.Error(w, "backup failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := atomicWrite(abs, next); err != nil {
		http.Error(w, "write failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	changed, err := repos.Write(d.root, generated)
	if err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]string{
			"error": "repos.yaml saved, generated files not refreshed (already rewritten: [" + strings.Join(changed, " ") + "]); change it again to finish: " + err.Error(),
		})
		return
	}
	cfg, err := buildReposConfig(next)
	if err != nil {
		writeJSONStatus(w, statusOf(err), map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"changed": true, "backup": backup, "generated": changed, "config": cfg})
}

// applyReposPatch edits the YAML tree and encodes it again. The encoder keeps
// comments and scalar styles; indentation is normalised to two spaces.
func applyReposPatch(data []byte, p reposPatch) ([]byte, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, httpError{http.StatusUnprocessableEntity, err.Error()}
	}
	if len(root.Content) != 1 || root.Content[0].Kind != yaml.MappingNode {
		return nil, httpError{http.StatusUnprocessableEntity, "repos.yaml must be a mapping"}
	}
	reposNode := mapValue(root.Content[0], "repos")
	if reposNode == nil || reposNode.Kind != yaml.MappingNode {
		return nil, httpError{http.StatusUnprocessableEntity, "repos.yaml has no repos mapping"}
	}
	if !repoNameOK.MatchString(p.Repo) {
		return nil, httpError{http.StatusBadRequest, fmt.Sprintf("%q is not an owner/repo name", p.Repo)}
	}

	switch p.Op {
	case "add":
		if mapValue(reposNode, p.Repo) != nil {
			return nil, httpError{http.StatusConflict, p.Repo + " is already listed"}
		}
		reposNode.Content = append(reposNode.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: p.Repo},
			&yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Style: yaml.FlowStyle})
	case "remove":
		if !deleteKey(reposNode, p.Repo) {
			return nil, httpError{http.StatusNotFound, p.Repo + " is not listed"}
		}
	case "", "set":
		block := mapValue(reposNode, p.Repo)
		if block == nil {
			return nil, httpError{http.StatusNotFound, p.Repo + " is not listed"}
		}
		if block.Kind != yaml.MappingNode {
			return nil, httpError{http.StatusUnprocessableEntity, p.Repo + " has no block to edit"}
		}
		if err := setKey(root.Content[0], block, p); err != nil {
			return nil, err
		}
	default:
		return nil, httpError{http.StatusBadRequest, "unknown op " + p.Op}
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&root); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func setKey(top, block *yaml.Node, p reposPatch) error {
	profiles := mapValue(top, "profiles")
	switch {
	case isToggle(p.Key):
		on, ok := p.Value.(bool)
		if !ok {
			return httpError{http.StatusBadRequest, p.Key + " takes true or false"}
		}
		fromProfile := profileEnables(profiles, block, p.Key)
		if on {
			if fromProfile && mapValue(block, p.Key) == nil {
				return nil // already on through a profile
			}
			if cur := mapValue(block, p.Key); cur != nil && !(cur.Kind == yaml.ScalarNode && cur.Tag == "!!bool" && cur.Value == "false") {
				return nil // already on, keep its options
			}
			putKey(block, p.Key, boolNode(true))
			return nil
		}
		// Off: a profile key needs an explicit false, anything else just goes.
		if fromProfile {
			putKey(block, p.Key, boolNode(false))
		} else {
			deleteKey(block, p.Key)
		}
	case p.Key == "rules":
		return setRules(block, p)
	case p.Key == "auto_merge":
		return setAutoMerge(profiles, block, p)
	case p.Key == "provider":
		if p.Value == nil || p.Value == "" {
			deleteKey(block, "provider")
			return nil
		}
		s, ok := p.Value.(string)
		if !ok || (s != "opencode" && s != "claude") {
			return httpError{http.StatusBadRequest, "provider must be opencode or claude"}
		}
		putKey(block, "provider", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s})
	default:
		return httpError{http.StatusBadRequest, fmt.Sprintf("the table does not edit %q: use the repos.yaml editor", p.Key)}
	}
	return nil
}

func isToggle(key string) bool {
	for _, t := range toggles {
		if t == key {
			return true
		}
	}
	return false
}

// profileEnables reports whether one of the block's `use` profiles switches the
// key on.
func profileEnables(profiles, block *yaml.Node, key string) bool {
	use := mapValue(block, "use")
	if use == nil || profiles == nil {
		return false
	}
	var names []string
	switch use.Kind {
	case yaml.ScalarNode:
		names = []string{use.Value}
	case yaml.SequenceNode:
		for _, n := range use.Content {
			names = append(names, n.Value)
		}
	}
	for _, name := range names {
		prof := mapValue(profiles, name)
		if prof == nil {
			continue
		}
		v := mapValue(prof, key)
		if v == nil {
			continue
		}
		if v.Kind == yaml.ScalarNode && v.Tag == "!!bool" && v.Value == "false" {
			continue
		}
		return true
	}
	return false
}

func boolNode(v bool) *yaml.Node {
	s := "false"
	if v {
		s = "true"
	}
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: s}
}

func mapValue(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// putKey sets key to value, replacing in place so order and comments stay.
func putKey(m *yaml.Node, key string, value *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			old := m.Content[i+1]
			value.HeadComment, value.LineComment, value.FootComment = old.HeadComment, old.LineComment, old.FootComment
			m.Content[i+1] = value
			return
		}
	}
	// A flow mapping such as `{}` grows into a block once it holds keys.
	m.Style &^= yaml.FlowStyle
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, value)
}

func deleteKey(m *yaml.Node, key string) bool {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return true
		}
	}
	return false
}

// ruleKeyOrder is the order a rule's keys are written in.
var ruleKeyOrder = []string{"on", "label", "only_authors", "ignore_authors", "scope", "labels", "inputs"}

// setRules writes the rules of a watcher feature: `true` for one empty rule, a
// block for one, a list for several. The feature must be one the table knows.
func setRules(block *yaml.Node, p reposPatch) error {
	if _, ok := repos.WatchSchemaOf(p.Feature); !ok {
		return httpError{http.StatusBadRequest, fmt.Sprintf("%q has no rules to edit", p.Feature)}
	}
	list, ok := p.Value.([]any)
	if !ok || len(list) == 0 {
		return httpError{http.StatusBadRequest, "rules take a non-empty list"}
	}
	items := make([]*yaml.Node, 0, len(list))
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			return httpError{http.StatusBadRequest, "each rule must be an object"}
		}
		node, err := ruleNode(m)
		if err != nil {
			return err
		}
		items = append(items, node)
	}
	var value *yaml.Node
	switch {
	case len(items) == 1 && len(items[0].Content) == 0:
		value = boolNode(true)
	case len(items) == 1:
		value = items[0]
	default:
		value = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for _, n := range items {
			if len(n.Content) == 0 {
				n = boolNode(true)
			}
			value.Content = append(value.Content, n)
		}
	}
	putKey(block, p.Feature, value)
	return nil
}

func ruleNode(m map[string]any) (*yaml.Node, error) {
	node := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	seen := map[string]bool{}
	add := func(key string) error {
		v, ok := m[key]
		if !ok || v == nil {
			return nil
		}
		seen[key] = true
		var vn yaml.Node
		if err := vn.Encode(v); err != nil {
			return httpError{http.StatusBadRequest, key + ": " + err.Error()}
		}
		if vn.Kind == yaml.SequenceNode {
			vn.Style = yaml.FlowStyle
		}
		kn := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
		if key == "on" {
			kn.Style = yaml.DoubleQuotedStyle // a YAML 1.1 reader takes a bare `on` for a boolean
		}
		node.Content = append(node.Content, kn, &vn)
		return nil
	}
	for _, key := range ruleKeyOrder {
		if err := add(key); err != nil {
			return nil, err
		}
	}
	var rest []string
	for key := range m {
		if !seen[key] {
			rest = append(rest, key)
		}
	}
	sort.Strings(rest)
	for _, key := range rest {
		if err := add(key); err != nil {
			return nil, err
		}
	}
	return node, nil
}

// setAutoMerge sets tend's auto_merge, turning `tend: true` into a block.
func setAutoMerge(profiles, block *yaml.Node, p reposPatch) error {
	on, ok := p.Value.(bool)
	if !ok {
		return httpError{http.StatusBadRequest, "auto_merge takes true or false"}
	}
	tend := mapValue(block, "tend")
	if tend == nil && !profileEnables(profiles, block, "tend") {
		return httpError{http.StatusConflict, "tend is switched off on this repository: tick Tend first"}
	}
	if tend == nil {
		// Tend comes from a profile: an own block with auto_merge overrides it.
		tend = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Style: yaml.FlowStyle}
		putKey(block, "tend", tend)
	}
	if tend.Kind == yaml.ScalarNode && tend.Tag == "!!bool" {
		if tend.Value == "false" {
			return httpError{http.StatusConflict, "tend is switched off on this repository"}
		}
		tend = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Style: yaml.FlowStyle}
		putKey(block, "tend", tend)
	}
	if tend.Kind != yaml.MappingNode {
		return httpError{http.StatusUnprocessableEntity, "tend has a shape the table does not edit"}
	}
	if on {
		putKey(tend, "auto_merge", boolNode(true))
	} else if profileAutoMerge(profiles, block) {
		// An inherited auto_merge is only switched off by saying false.
		putKey(tend, "auto_merge", boolNode(false))
	} else {
		deleteKey(tend, "auto_merge")
		if len(tend.Content) == 0 {
			*tend = *boolNode(true)
		}
	}
	return nil
}

// profileAutoMerge reports whether a `use` profile sets tend.auto_merge to true.
func profileAutoMerge(profiles, block *yaml.Node) bool {
	for _, name := range profileNames(block) {
		tend := mapValue(mapValue(profiles, name), "tend")
		if v := mapValue(tend, "auto_merge"); v != nil && v.Value == "true" {
			return true
		}
	}
	return false
}

func profileNames(block *yaml.Node) []string {
	use := mapValue(block, "use")
	if use == nil {
		return nil
	}
	if use.Kind == yaml.ScalarNode {
		return []string{use.Value}
	}
	var names []string
	for _, n := range use.Content {
		names = append(names, n.Value)
	}
	return names
}
