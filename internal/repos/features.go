package repos

import (
	"fmt"
	"sort"
	"strings"
)

// Rule is one entry of the generated awf-gh.yaml.
type Rule struct {
	Repo          string
	Workflow      string
	Scope         string
	On            []string
	IgnoreAuthors []string
	OnlyAuthors   []string
	Labels        []string
	Inputs        map[string]string
	// IgnoreFirstContribution drops the events of an author whose first
	// contribution to the repository this is (case A5).
	IgnoreFirstContribution bool

	// Executor names the awf serve that runs the task, from the executor
	// feature; empty means the watcher's own awf_url.
	Executor string

	// ProviderAware says the workflow declares a `provider` input, so the
	// provider feature may set it. A modifier never touches other rules.
	ProviderAware bool
}

// TendEntry is one line of the generated tend.list.
type TendEntry struct {
	Repo      string
	AutoMerge bool
}

// Output is everything the features add up to. Files carries extra generated
// files a feature owns: its name is the file, its lines the content.
type Output struct {
	Rules    []Rule
	Tend     []TendEntry
	Checkout []CheckoutEntry
	Files    map[string][]string
}

// CheckoutEntry is one line of the generated repos.map.
type CheckoutEntry struct {
	Repo string
	Path string
}

// Feature is one key under a repository. It is the extension point: add a
// type that satisfies it and register it from an init function, and repos.yaml
// accepts the key, `bin/repos features` documents it, and the generated files
// carry what it emits. Embed `base` to get the two optional methods as no-ops.
type Feature interface {
	// Key is the name used under a repository, under defaults and in profiles.
	Key() string
	// Doc is one line for `bin/repos features`.
	Doc() string
	// Check validates a value (after defaults and profiles). It is called only
	// for an enabled feature.
	Check(value any) error
	// Emit adds this feature's contribution for one repository.
	Emit(r *Resolved, out *Output) error
	// Modify changes what the features emitted for the same repository, from
	// index `from` of out.Rules on (as provider does), instead of or as well as
	// emitting its own. It runs after every Emit of that repository.
	Modify(r *Resolved, out *Output, from int) error
	// Workflow names the awf-gh workflow whose rules this feature owns, so
	// `bin/repos import` can read them back; empty for a feature that is not a
	// watcher rule.
	Workflow() string
}

// base holds the optional methods of Feature, for a feature that needs neither.
type base struct{}

func (base) Modify(*Resolved, *Output, int) error { return nil }
func (base) Workflow() string                     { return "" }

var (
	registry = map[string]Feature{}
	order    []string
)

// Register adds a feature. It panics on a duplicate key: that is a programming
// error found by the first test run, not a runtime condition.
func Register(f Feature) {
	if _, dup := registry[f.Key()]; dup {
		panic("repos: feature registered twice: " + f.Key())
	}
	registry[f.Key()] = f
	order = append(order, f.Key())
}

// FeatureKeys lists the registered keys, in registration order.
func FeatureKeys() []string { return append([]string(nil), order...) }

// FeatureDocs returns "key: doc" lines for the CLI.
func FeatureDocs() []string {
	out := make([]string, 0, len(order))
	for _, key := range order {
		out = append(out, fmt.Sprintf("%-10s %s", key, registry[key].Doc()))
	}
	return out
}

// Generate resolves the document and runs every enabled feature, repository by
// repository, in registration order, then the modifiers.
func Generate(doc *Document) (*Output, error) {
	resolved, err := Resolve(doc)
	if err != nil {
		return nil, err
	}
	out := &Output{Files: map[string][]string{}}
	var problems []string
	for _, r := range resolved {
		from := len(out.Rules)
		for _, key := range order {
			value, ok := r.Values[key]
			if !ok {
				continue
			}
			f := registry[key]
			if err := f.Check(value); err != nil {
				problems = append(problems, fmt.Sprintf("%s: %s: %v", r.Name, key, err))
				continue
			}
			if err := f.Emit(r, out); err != nil {
				problems = append(problems, fmt.Sprintf("%s: %s: %v", r.Name, key, err))
			}
		}
		for _, key := range order {
			if _, ok := r.Values[key]; !ok {
				continue
			}
			if err := registry[key].Modify(r, out, from); err != nil {
				problems = append(problems, fmt.Sprintf("%s: %s: %v", r.Name, key, err))
			}
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return nil, fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	return out, nil
}
