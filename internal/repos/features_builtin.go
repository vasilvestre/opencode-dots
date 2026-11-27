package repos

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"
)

// The events awf-gh knows. A rule naming another one would stop the whole
// polling pass, so it is refused here, before anything is generated.
var events = []string{"pr-opened", "pr-updated", "checks-failed", "review-comment", "issue-opened", "issue-labeled", "command"}

func init() {
	Register(checkout{})
	Register(tend{})
	Register(watch{key: "review", workflow: "review-pr", defaultOn: []string{"pr-opened", "pr-updated", "checks-failed"}, providerAware: true,
		doc: "review-pr on pull requests: true, a block, or a list of blocks (on, ignore_authors, only_authors, scope, labels, inputs)"})
	Register(watch{key: "reply", workflow: "reply-pr", defaultOn: []string{"review-comment"},
		doc: "reply-pr on review comments of my own pull requests (same options as review)"})
	Register(watch{key: "fix_ci", workflow: "fix-ci", defaultOn: []string{"checks-failed"},
		doc: "fix-ci on failed checks of my own pull requests (same options as review)"})
	Register(watch{key: "fix_issue", workflow: "fix-issue", defaultOn: []string{"issue-opened"}, label: true,
		doc: "fix-issue on new issues, or on one label with `label: dot` (same options as review)"})
	Register(provider{})
}

// --- checkout -> repos.map ---------------------------------------------------

type checkout struct{ base }

func (checkout) Key() string { return "checkout" }
func (checkout) Doc() string {
	return "absolute host path of an existing checkout the workflows reuse instead of cloning (repos.map)"
}

func (checkout) Check(v any) error {
	path, ok := v.(string)
	if !ok {
		return errors.New("must be an absolute path")
	}
	if strings.ContainsFunc(path, unicode.IsControl) {
		return fmt.Errorf("%q contains a control character; remove it", path)
	}
	if !strings.HasPrefix(path, "/") || strings.ContainsAny(path, "=") {
		return fmt.Errorf("%q is not an absolute path", path)
	}
	return nil
}

func (checkout) Emit(r *Resolved, out *Output) error {
	out.Checkout = append(out.Checkout, CheckoutEntry{Repo: r.Name, Path: r.Values["checkout"].(string)})
	return nil
}

// WatchSchema is what a watcher feature (review, reply, fix_issue, fix_ci)
// accepts, for a form that edits its rules.
type WatchSchema struct {
	Events    []string `json:"events"`
	DefaultOn []string `json:"default_on"`
	// Label is true when the feature takes the `label` shorthand (fix_issue).
	Label bool `json:"label"`
}

// WatchSchemaOf describes a watcher feature; ok is false for any other key.
func WatchSchemaOf(key string) (WatchSchema, bool) {
	w, ok := registry[key].(watch)
	if !ok {
		return WatchSchema{}, false
	}
	return WatchSchema{Events: slices.Clone(events), DefaultOn: slices.Clone(w.defaultOn), Label: w.label}, true
}

// --- tend -> tend.list -------------------------------------------------------

type tend struct{ base }

func (tend) Key() string { return "tend" }
func (tend) Doc() string {
	return "daily tend-repo pass: true, or {auto_merge: true} to merge when review-pr says merge (tend.list)"
}

func (tend) Check(v any) error {
	m, ok := v.(map[string]any)
	if !ok {
		return errors.New("must be true or a block")
	}
	for key, value := range m {
		if key != "auto_merge" {
			return fmt.Errorf("unknown option %q (want auto_merge)", key)
		}
		if _, ok := value.(bool); !ok {
			return errors.New("auto_merge must be true or false")
		}
	}
	return nil
}

func (tend) Emit(r *Resolved, out *Output) error {
	auto, _ := r.Values["tend"].(map[string]any)["auto_merge"].(bool)
	level, up, idn := tendExtras(r)
	out.Tend = append(out.Tend, TendEntry{Repo: r.Name, AutoMerge: auto, Autonomy: level, Upstream: up, Identity: idn, Budget: budgetCap(r)})
	return nil
}

// --- watch rules -> awf-gh.yaml ---------------------------------------------

type watch struct {
	base
	key           string
	workflow      string
	defaultOn     []string
	providerAware bool
	label         bool
	doc           string
}

func (w watch) Key() string { return w.key }
func (w watch) Doc() string { return w.doc }

func (w watch) Check(v any) error {
	_, err := w.rules("", v)
	return err
}

func (w watch) Emit(r *Resolved, out *Output) error {
	rules, err := w.rules(r.Name, r.Values[w.key])
	if err != nil {
		return err
	}
	out.Rules = append(out.Rules, rules...)
	return nil
}

// rules turns a feature value into awf-gh rules: one for a block, several for a
// list of blocks. `true` inside a list is a block with only the defaults.
func (w watch) rules(repo string, v any) ([]Rule, error) {
	var items []any
	switch x := v.(type) {
	case map[string]any:
		items = []any{x}
	case []any:
		items = x
		if len(items) == 0 {
			return nil, errors.New("a list needs at least one block")
		}
	default:
		return nil, errors.New("must be true, a block or a list of blocks")
	}
	var out []Rule
	for i, item := range items {
		if item == true || item == nil {
			item = map[string]any{}
		}
		block, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("item %d must be a block", i+1)
		}
		rule, err := w.rule(repo, block)
		if err != nil {
			if len(items) > 1 {
				return nil, fmt.Errorf("item %d: %w", i+1, err)
			}
			return nil, err
		}
		out = append(out, rule)
	}
	return out, nil
}

func (w watch) rule(repo string, block map[string]any) (Rule, error) {
	rule := Rule{Repo: repo, Workflow: w.workflow, ProviderAware: w.providerAware}
	known := []string{"on", "ignore_authors", "only_authors", "scope", "labels", "inputs"}
	if w.label {
		known = append(known, "label")
	}
	var unknown []string
	for key := range block {
		if !slices.Contains(known, key) {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) > 0 {
		slices.Sort(unknown)
		return rule, fmt.Errorf("unknown option %s (want %s)", strings.Join(unknown, ", "), strings.Join(known, ", "))
	}
	var err error
	if rule.On, err = stringList(block, "on"); err != nil {
		return rule, err
	}
	if rule.IgnoreAuthors, err = stringList(block, "ignore_authors"); err != nil {
		return rule, err
	}
	if rule.OnlyAuthors, err = stringList(block, "only_authors"); err != nil {
		return rule, err
	}
	if rule.Labels, err = stringList(block, "labels"); err != nil {
		return rule, err
	}
	if s, ok := block["scope"]; ok {
		scope, isString := s.(string)
		if !isString || scope == "" {
			return rule, errors.New("scope must be a non-empty string")
		}
		rule.Scope = scope
	}
	if in, ok := block["inputs"]; ok {
		m, isMap := in.(map[string]any)
		if !isMap {
			return rule, errors.New("inputs must be a mapping")
		}
		rule.Inputs = map[string]string{}
		for k, val := range m {
			switch val.(type) {
			case string, bool, int, int64, float64:
				rule.Inputs[k] = fmt.Sprint(val)
			default:
				return rule, fmt.Errorf("input %q must be a string, a number or a boolean", k)
			}
		}
	}
	if label, ok := block["label"]; ok {
		name, isString := label.(string)
		if !isString || name == "" {
			return rule, errors.New("label must be a non-empty string")
		}
		if len(rule.On) > 0 || len(rule.Labels) > 0 {
			return rule, errors.New("label is a shorthand for on: [issue-labeled] and labels: [<label>]; do not combine it with on or labels")
		}
		rule.On = []string{"issue-labeled"}
		rule.Labels = []string{name}
	}
	if len(rule.On) == 0 {
		rule.On = slices.Clone(w.defaultOn)
	}
	for _, ev := range rule.On {
		if !slices.Contains(events, ev) {
			return rule, fmt.Errorf("unknown event %q (want %s)", ev, strings.Join(events, ", "))
		}
	}
	if slices.Contains(rule.On, "issue-labeled") && len(rule.Labels) == 0 {
		return rule, errors.New("issue-labeled needs a label (label: <name>)")
	}
	return rule, nil
}

func stringList(block map[string]any, key string) ([]string, error) {
	raw, ok := block[key]
	if !ok || raw == nil {
		return nil, nil
	}
	list, isList := raw.([]any)
	if !isList {
		return nil, fmt.Errorf("%s must be a list of strings", key)
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		s, isString := item.(string)
		if !isString || s == "" {
			return nil, fmt.Errorf("%s must be a list of strings", key)
		}
		out = append(out, s)
	}
	return out, nil
}

// --- provider ----------------------------------------------------------------

// provider picks the agent CLI for the repository. It emits nothing itself: it
// sets the `provider` input on the rules whose workflow declares one, so a
// workflow that does not support it is never handed an input it would ignore.
type provider struct{}

func (provider) Key() string { return "provider" }
func (provider) Doc() string {
	return "agent CLI for this repository: opencode (default) or claude, passed as the provider input of the workflows that take one"
}

func (provider) Check(v any) error {
	s, ok := v.(string)
	if !ok || (s != "opencode" && s != "claude") {
		return errors.New("must be opencode or claude")
	}
	return nil
}

func (provider) Emit(*Resolved, *Output) error { return nil }

func (provider) Modify(r *Resolved, out *Output, from int) error {
	name := r.Values["provider"].(string)
	if name == "opencode" {
		return nil // the workflows default to it
	}
	for i := from; i < len(out.Rules); i++ {
		rule := &out.Rules[i]
		if !rule.ProviderAware {
			continue
		}
		if rule.Inputs == nil {
			rule.Inputs = map[string]string{}
		}
		if _, set := rule.Inputs["provider"]; !set {
			rule.Inputs["provider"] = name
		}
	}
	return nil
}
