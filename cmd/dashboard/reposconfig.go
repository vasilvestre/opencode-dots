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

// providers are the values of a repository's `provider`; "any" leaves the choice to the worker that
// takes the job, so it needs an executor and is never a default for every repository.
var providers = []string{"opencode", "claude", "any"}

var defaultProviders = providers[:2]

// repoless are the operations on the file's own blocks, not on one repository.
var repoless = map[string]bool{"default_provider": true, "settings": true, "default_authors": true}

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
	// Executor names the worker queue that runs this repository's tasks; "any" needs one.
	Executor string `json:"executor,omitempty"`
	// Trust is the level of ADR 0032, empty for the default `open`.
	Trust string `json:"trust,omitempty"`
	// What the dot may do here and how it is held back; each is the effective value, after
	// defaults and profiles, so the page shows what the workflows will see.
	Autonomy       string   `json:"autonomy,omitempty"`
	Identity       string   `json:"identity,omitempty"`
	CircuitBreaker bool     `json:"circuit_breaker,omitempty"`
	BudgetUSD      *float64 `json:"budget_usd,omitempty"`
	Sandbox        *sandbox `json:"sandbox,omitempty"`
	// Work sources polled for this repository: the Jira project key and the Sentry project.
	Jira       []string `json:"jira,omitempty"`
	Sentry     []string `json:"sentry,omitempty"`
	Advisories bool     `json:"advisories,omitempty"`
}

// sandbox is the part of the `sandbox` block the page edits: the test command. The rest of the
// block (image, dockerfile, limits) is shown as a summary and kept as it is on a save.
type sandbox struct {
	Command string `json:"command,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

var (
	autonomyLevels = []string{"observe", "propose", "act", "auto"}
	identityLevels = []string{"off", "marker", "signed"}
)

type reposConfig struct {
	Repos     []repoRow `json:"repos"`
	Profiles  []string  `json:"profiles"`
	Toggles   []string  `json:"toggles"`
	Providers []string  `json:"providers"`
	// DefaultProviders are the agents offered as the default for every repository.
	DefaultProviders []string `json:"default_providers"`
	// DefaultProvider is defaults.provider of repos.yaml, empty for the built-in opencode.
	DefaultProvider string `json:"default_provider"`
	// Settings is the machine's own block of repos.yaml (watcher knobs, trust, executors).
	Settings repos.Settings       `json:"settings"`
	Forms    map[string]watchForm `json:"forms"`
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
	cfg := &reposConfig{Repos: []repoRow{}, Profiles: []string{}, Toggles: toggles, Providers: providers, DefaultProviders: defaultProviders, Forms: map[string]watchForm{}}
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
	cfg.DefaultProvider, _ = doc.Defaults["provider"].(string)
	cfg.Settings = doc.Settings
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
		row.Autonomy = stringValue(res, "autonomy")
		row.Identity = stringValue(res, "identity")
		row.Executor = stringValue(res, "executor")
		row.Jira = projectsValue(res, "jira")
		row.Sentry = projectsValue(res, "sentry")
		if v, _ := res.Value("advisories"); v != nil && v != false {
			row.Advisories = true
		}
		if v, _ := res.Value("circuit_breaker"); v != nil && v != false {
			row.CircuitBreaker = true
		}
		if v, _ := res.Value("budget"); v != nil {
			if m, ok := v.(map[string]any); ok {
				if n, ok := toFloat(m["daily_usd"]); ok {
					row.BudgetUSD = &n
				}
			}
		}
		if v, on := res.Value("sandbox"); on && v != false && v != nil {
			sb := &sandbox{}
			if m, ok := v.(map[string]any); ok {
				if test, ok := m["test"].(map[string]any); ok {
					sb.Command, _ = test["command"].(string)
					var parts []string
					for _, k := range []string{"image", "dockerfile", "target"} {
						if x, _ := test[k].(string); x != "" {
							parts = append(parts, x)
						}
					}
					sb.Detail = strings.Join(parts, " · ")
				}
			}
			row.Sandbox = sb
		}
		cfg.Repos = append(cfg.Repos, row)
	}
	return cfg, nil
}

func stringValue(res *repos.Resolved, key string) string {
	v, _ := res.Value(key)
	s, _ := v.(string)
	return s
}

// projectsValue is the project list of a tracker key: one repository may take several projects.
func projectsValue(res *repos.Resolved, key string) []string {
	v, _ := res.Value(key)
	return repos.Projects(v)
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
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
	Op   string `json:"op"`   // "set" (default), "add", "remove", "default_provider"
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
	if !repoless[p.Op] && !repoNameOK.MatchString(p.Repo) {
		return nil, httpError{http.StatusBadRequest, fmt.Sprintf("%q is not an owner/repo name", p.Repo)}
	}
	before, err := doc.Marshal()
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(doc.Repos, func(e repos.Entry) bool { return e.Name == p.Repo })
	switch p.Op {
	case "default_provider":
		name, _ := p.Value.(string)
		switch {
		case name == "":
			delete(doc.Defaults, "provider")
		case slices.Contains(defaultProviders, name):
			doc.Defaults["provider"] = name
		default:
			return nil, httpError{http.StatusBadRequest, "the default agent is opencode or claude: \"any\" is set on a repository that has an executor"}
		}
	case "settings":
		if err := setSetting(doc, p); err != nil {
			return nil, err
		}
	case "default_authors":
		if err := setDefaultAuthors(doc, p); err != nil {
			return nil, err
		}
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

// setSetting edits the settings the page owns; repos.Settings.Set holds the rules.
func setSetting(doc *repos.Document, p reposPatch) error {
	if err := doc.Settings.Set(p.Key, p.Value); err != nil {
		return httpError{http.StatusBadRequest, err.Error()}
	}
	return nil
}

// setDefaultAuthors writes defaults.<feature>.ignore_authors or only_authors: the authors every
// repository inherits, which a repository's own rule can still replace.
func setDefaultAuthors(doc *repos.Document, p reposPatch) error {
	if _, ok := repos.WatchSchemaOf(p.Feature); !ok {
		return httpError{http.StatusBadRequest, fmt.Sprintf("%q has no author lists", p.Feature)}
	}
	if p.Key != "ignore_authors" && p.Key != "only_authors" {
		return httpError{http.StatusBadRequest, "authors are ignore_authors or only_authors"}
	}
	names := repos.Projects(p.Value) // a list of strings, whatever they name
	if p.Value != nil && names == nil {
		return httpError{http.StatusBadRequest, p.Key + " takes a list of logins"}
	}
	names = slices.DeleteFunc(names, func(s string) bool { return strings.TrimSpace(s) == "" })
	block, _ := doc.Defaults[p.Feature].(map[string]any)
	if block == nil {
		block = map[string]any{}
	}
	if len(names) == 0 {
		delete(block, p.Key)
	} else {
		items := make([]any, len(names))
		for i, n := range names {
			items[i] = strings.TrimSpace(n)
		}
		block[p.Key] = items
	}
	if len(block) == 0 {
		delete(doc.Defaults, p.Feature)
	} else {
		doc.Defaults[p.Feature] = block
	}
	return nil
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
	case p.Key == "autonomy" || p.Key == "identity":
		return setChoice(cfg, p.Key, p.Value, map[string][]string{"autonomy": autonomyLevels, "identity": identityLevels}[p.Key])
	case p.Key == "jira" || p.Key == "sentry":
		// A name or a list of names; the format is checked when the document is built.
		list := repos.Projects(p.Value)
		if _, isString := p.Value.(string); p.Value != nil && !isString && list == nil {
			return httpError{http.StatusBadRequest, p.Key + " takes a project name or a list of them"}
		}
		list = slices.DeleteFunc(list, func(s string) bool { return strings.TrimSpace(s) == "" })
		switch len(list) {
		case 0:
			delete(cfg, p.Key)
		case 1:
			cfg[p.Key] = strings.TrimSpace(list[0])
		default:
			items := make([]any, len(list))
			for i, name := range list {
				items[i] = strings.TrimSpace(name)
			}
			cfg[p.Key] = items
		}
	case p.Key == "circuit_breaker" || p.Key == "advisories":
		on, ok := p.Value.(bool)
		if !ok {
			return httpError{http.StatusBadRequest, p.Key + " takes true or false"}
		}
		switch {
		case on:
			cfg[p.Key] = true
		case profileEnables(doc, cfg, p.Key):
			cfg[p.Key] = false
		default:
			delete(cfg, p.Key)
		}
	case p.Key == "budget":
		if p.Value == nil {
			delete(cfg, "budget")
			return nil
		}
		n, ok := toFloat(p.Value)
		if !ok {
			return httpError{http.StatusBadRequest, "budget takes a number of dollars per day, or null"}
		}
		cfg["budget"] = map[string]any{"daily_usd": n}
	case p.Key == "sandbox":
		on, ok := p.Value.(bool)
		if !ok {
			return httpError{http.StatusBadRequest, "sandbox takes true or false"}
		}
		switch {
		case on && cfg["sandbox"] != nil && cfg["sandbox"] != false:
			// already on, keep its options
		case on:
			cfg["sandbox"] = true
		case profileEnables(doc, cfg, p.Key):
			cfg["sandbox"] = false
		default:
			delete(cfg, "sandbox")
		}
	case p.Key == "sandbox_command":
		return setSandboxCommand(cfg, p.Value)
	case p.Key == "provider":
		if p.Value == nil || p.Value == "" {
			delete(cfg, "provider")
			return nil
		}
		if s, ok := p.Value.(string); !ok || !slices.Contains(providers, s) {
			return httpError{http.StatusBadRequest, "provider must be opencode, claude or any"}
		}
		cfg["provider"] = p.Value
	default:
		return httpError{http.StatusBadRequest, fmt.Sprintf("the table does not edit %q: use the repos.yaml editor", p.Key)}
	}
	return nil
}

// setChoice writes a key that takes one of a few words; null or "" removes it.
func setChoice(cfg map[string]any, key string, value any, allowed []string) error {
	if value == nil || value == "" {
		delete(cfg, key)
		return nil
	}
	name, _ := value.(string)
	if !slices.Contains(allowed, name) {
		return httpError{http.StatusBadRequest, fmt.Sprintf("%s must be one of %s", key, strings.Join(allowed, ", "))}
	}
	cfg[key] = name
	return nil
}

// setSandboxCommand sets sandbox.test.command and leaves the rest of the block alone. An empty
// command removes it (and the test block, once empty); the block stays, since it still switches
// the check on.
func setSandboxCommand(cfg map[string]any, value any) error {
	cmd, ok := value.(string)
	if !ok && value != nil {
		return httpError{http.StatusBadRequest, "the sandbox command is a string"}
	}
	block, _ := cfg["sandbox"].(map[string]any)
	if block == nil {
		if cfg["sandbox"] != true {
			return httpError{http.StatusConflict, "the sandbox is off on this repository: switch it on first"}
		}
		block = map[string]any{}
	}
	test, _ := block["test"].(map[string]any)
	if test == nil {
		test = map[string]any{}
	}
	if cmd == "" {
		delete(test, "command")
	} else {
		test["command"] = cmd
	}
	if len(test) == 0 {
		delete(block, "test")
	} else {
		block["test"] = test
	}
	if len(block) == 0 {
		cfg["sandbox"] = true
	} else {
		cfg["sandbox"] = block
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
