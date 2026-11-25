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
}

type repoRow struct {
	Name     string                  `json:"name"`
	Use      []string                `json:"use,omitempty"`
	Features map[string]featureState `json:"features"`
	Provider string                  `json:"provider"`
	// ProviderSource is "repo", "default" or "" (the built-in opencode).
	ProviderSource string `json:"provider_source,omitempty"`
	Checkout       string `json:"checkout,omitempty"`
}

type reposConfig struct {
	Repos     []repoRow `json:"repos"`
	Profiles  []string  `json:"profiles"`
	Toggles   []string  `json:"toggles"`
	Providers []string  `json:"providers"`
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
	cfg := &reposConfig{Repos: []repoRow{}, Profiles: []string{}, Toggles: toggles, Providers: providers}
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
			if v, on := res.Values[key]; on {
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
			}
			row.Features[key] = st
		}
		if v, ok := res.Values["provider"].(string); ok && v != "" {
			row.Provider = v
			if _, own := entry.Config["provider"]; own {
				row.ProviderSource = "repo"
			} else {
				row.ProviderSource = "default"
			}
		}
		if v, ok := res.Values["checkout"].(string); ok {
			row.Checkout = v
		}
		cfg.Repos = append(cfg.Repos, row)
	}
	return cfg, nil
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
	Op    string `json:"op"`   // "set" (default), "add", "remove"
	Repo  string `json:"repo"` // owner/name
	Key   string `json:"key"`
	Value any    `json:"value"` // bool for a toggle, string for provider and checkout, null to clear
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
