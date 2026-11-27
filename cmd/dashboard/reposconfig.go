package main

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"opencode-dots/internal/repos"
)

// The repositories table edits repos.yaml one key at a time: it parses the file, changes the
// decoded document and writes it again with Document.Marshal, so the first change reformats the
// file (comments are not kept; keys are sorted). Every change goes through repos.Build before it
// reaches the disk, exactly like a save from the editor: a refused change leaves the file as it was.

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
	var he httpError
	if errors.As(err, &he) {
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
	slices.Sort(cfg.Profiles)
	for i, entry := range doc.Repos {
		res := resolved[i]
		row := repoRow{Name: entry.Name, Features: map[string]featureState{}, Provider: "opencode"}
		row.Use = append(row.Use, useNames(entry.Config["use"])...)
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
	auditTarget(r, p.Repo+" "+cmp.Or(p.Key, p.Op))
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

// applyReposPatch edits the parsed document and writes it again with Document.Marshal. A change
// that leaves the document as it was returns data untouched, so a no-op keeps the file as it is.
func applyReposPatch(data []byte, p reposPatch) ([]byte, error) {
	doc, err := repos.Parse(data)
	if err != nil {
		return nil, httpError{http.StatusUnprocessableEntity, err.Error()}
	}
	if !repoNameOK.MatchString(p.Repo) {
		return nil, httpError{http.StatusBadRequest, fmt.Sprintf("%q is not an owner/repo name", p.Repo)}
	}
	before, err := doc.Marshal()
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(doc.Repos, func(e repos.Entry) bool { return e.Name == p.Repo })
	switch p.Op {
	case "add":
		if i >= 0 {
			return nil, httpError{http.StatusConflict, p.Repo + " is already listed"}
		}
		doc.Repos = append(doc.Repos, repos.Entry{Name: p.Repo, Config: map[string]any{}})
	case "remove":
		if i < 0 {
			return nil, httpError{http.StatusNotFound, p.Repo + " is not listed"}
		}
		doc.Repos = slices.Delete(doc.Repos, i, i+1)
	case "", "set":
		if i < 0 {
			return nil, httpError{http.StatusNotFound, p.Repo + " is not listed"}
		}
		if err := setKey(doc, doc.Repos[i].Config, p); err != nil {
			return nil, err
		}
	default:
		return nil, httpError{http.StatusBadRequest, "unknown op " + p.Op}
	}
	after, err := doc.Marshal()
	if err != nil {
		return nil, err
	}
	if bytes.Equal(before, after) {
		return data, nil
	}
	return after, nil
}

func setKey(doc *repos.Document, cfg map[string]any, p reposPatch) error {
	switch {
	case slices.Contains(toggles, p.Key):
		on, ok := p.Value.(bool)
		if !ok {
			return httpError{http.StatusBadRequest, p.Key + " takes true or false"}
		}
		fromProfile := profileEnables(doc, cfg, p.Key)
		cur, own := cfg[p.Key]
		switch {
		case on && (own && cur != false || fromProfile && !own):
			// already on, keep its options
		case on:
			cfg[p.Key] = true
		case fromProfile: // a profile key needs an explicit false
			cfg[p.Key] = false
		default:
			delete(cfg, p.Key)
		}
	case p.Key == "rules":
		return setRules(cfg, p)
	case p.Key == "auto_merge":
		return setAutoMerge(doc, cfg, p)
	case p.Key == "provider":
		if p.Value == nil || p.Value == "" {
			delete(cfg, "provider")
			return nil
		}
		if s, ok := p.Value.(string); !ok || !slices.Contains(providers, s) {
			return httpError{http.StatusBadRequest, "provider must be opencode or claude"}
		}
		cfg["provider"] = p.Value
	default:
		return httpError{http.StatusBadRequest, fmt.Sprintf("the table does not edit %q: use the repos.yaml editor", p.Key)}
	}
	return nil
}

// profileEnables reports whether one of the repository's `use` profiles switches the key on.
func profileEnables(doc *repos.Document, cfg map[string]any, key string) bool {
	for _, name := range useNames(cfg["use"]) {
		if v, ok := doc.Profiles[name][key]; ok && v != false {
			return true
		}
	}
	return false
}

// setRules writes the rules of a watcher feature: `true` for one empty rule, a block for one, a
// list for several. The feature must be one the table knows.
func setRules(cfg map[string]any, p reposPatch) error {
	if _, ok := repos.WatchSchemaOf(p.Feature); !ok {
		return httpError{http.StatusBadRequest, fmt.Sprintf("%q has no rules to edit", p.Feature)}
	}
	list, ok := p.Value.([]any)
	if !ok || len(list) == 0 {
		return httpError{http.StatusBadRequest, "rules take a non-empty list"}
	}
	rules := make([]any, 0, len(list))
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			return httpError{http.StatusBadRequest, "each rule must be an object"}
		}
		rule := map[string]any{}
		for k, v := range m {
			if v != nil {
				rule[k] = v
			}
		}
		if len(rule) == 0 {
			rules = append(rules, true)
		} else {
			rules = append(rules, rule)
		}
	}
	if len(rules) == 1 {
		cfg[p.Feature] = rules[0]
	} else {
		cfg[p.Feature] = rules
	}
	return nil
}

// setAutoMerge sets tend's auto_merge, turning `tend: true` into a block.
func setAutoMerge(doc *repos.Document, cfg map[string]any, p reposPatch) error {
	on, ok := p.Value.(bool)
	if !ok {
		return httpError{http.StatusBadRequest, "auto_merge takes true or false"}
	}
	cur, own := cfg["tend"]
	tend, isMap := cur.(map[string]any)
	switch {
	case !own && !profileEnables(doc, cfg, "tend"):
		return httpError{http.StatusConflict, "tend is switched off on this repository: tick Tend first"}
	case !own || cur == true: // from a profile or a bare true: an own block with auto_merge overrides it
		tend = map[string]any{}
	case cur == false:
		return httpError{http.StatusConflict, "tend is switched off on this repository"}
	case !isMap:
		return httpError{http.StatusUnprocessableEntity, "tend has a shape the table does not edit"}
	}
	switch {
	case on:
		tend["auto_merge"] = true
	case profileAutoMerge(doc, cfg): // an inherited auto_merge is only switched off by saying false
		tend["auto_merge"] = false
	default:
		delete(tend, "auto_merge")
	}
	cfg["tend"] = tend // an empty block is written as true
	return nil
}

// profileAutoMerge reports whether a `use` profile sets tend.auto_merge to true.
func profileAutoMerge(doc *repos.Document, cfg map[string]any) bool {
	for _, name := range useNames(cfg["use"]) {
		if tend, _ := doc.Profiles[name]["tend"].(map[string]any); tend["auto_merge"] == true {
			return true
		}
	}
	return false
}
