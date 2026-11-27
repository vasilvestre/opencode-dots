// Package repos holds the one list of repositories the dot works on and what it
// does on each of them. repos.yaml is the source of truth; the files the other
// tools read (awf-gh.yaml, tend.list, repos.map) are generated from it.
//
// Every key under a repository is a Feature registered in features.go. A
// feature checks its own value and says what it adds to the generated files, so
// a new capability is one new file, not a change to the loader.
package repos

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// Version is the only repos.yaml version this code reads.
const Version = 1

var repoNameRE = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// Settings are the awf-gh knobs that belong to the watcher, not to a repository.
// An empty AwfURL or StatePath means "keep what the generated file already has,
// else the built-in default": bin/configure writes the mode dependent values.
type Settings struct {
	AwfURL    string `yaml:"awf_url"`
	StatePath string `yaml:"state_path"`
	MaxPerRun int    `yaml:"max_per_run"`
	// Executors maps a name to the awf serve that runs the tasks of the
	// repositories naming it (ADR 0031). The watcher stays where it is.
	Executors map[string]string `yaml:"executors"`
	// Trust is what this machine may hold and what a client repository may use
	// (ADR 0032). It is a machine setting, like awf_url.
	Trust TrustSettings `yaml:"trust"`
	// DotID is this server's dot id (ADR 0038). A repository whose `dot` names another
	// id is not acted on here. Empty means every repository is this server's.
	DotID string `yaml:"dot_id"`
	// CheckoutHints suggests where a developer keeps the projects of an owner: `{acme: "~/acme"}`. A worker
	// gets it from the server and shows it to someone who has not cloned a project; it is never imposed
	// (docs/paths.md).
	CheckoutHints map[string]string `yaml:"checkout_hints"`
}

// TrustSettings is settings.trust (ADR 0032). Host is the highest level this
// machine may hold: open, client, private or strict. Allow names what a repository at
// client or strict may reach.
type TrustSettings struct {
	Host  string     `yaml:"host"`
	Allow TrustAllow `yaml:"allow"`
	// Region is where this machine runs; Regions says where each named executor runs. A repository at
	// client or above needs Europe (a value starting with "eu"). ZDR names the model providers the owner
	// has a zero data retention guarantee from (ADR 0050): the prefix of a model id before "/", or
	// "anthropic" for the claude CLI. SelfHosted names the model providers the owner runs
	// themselves, which count as zero retention and are all a private or strict repository may use.
	Region     string            `yaml:"region"`
	Regions    map[string]string `yaml:"regions"`
	ZDR        []string          `yaml:"zdr"`
	SelfHosted []string          `yaml:"self_hosted"`
}

// TrustAllow is the allow-list of settings.trust.allow: the providers, the
// models and the executors a client or strict repository may use.
type TrustAllow struct {
	Providers []string `yaml:"providers"`
	Models    []string `yaml:"models"`
	Executors []string `yaml:"executors"`
}

// Entry is one repository, in file order, with what its block says before any
// default or profile is applied.
type Entry struct {
	Name   string
	Config map[string]any
}

// Document is a parsed repos.yaml.
type Document struct {
	Settings Settings
	Defaults map[string]any
	Profiles map[string]map[string]any
	Repos    []Entry
}

var topKeys = map[string]bool{"version": true, "settings": true, "defaults": true, "profiles": true, "repos": true}

// Parse reads a repos.yaml. It refuses unknown top-level keys and duplicate
// repositories, because a typo there would silently drop a repository.
func Parse(data []byte) (*Document, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var root yaml.Node
	if err := dec.Decode(&root); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("repos.yaml is empty")
		}
		return nil, fmt.Errorf("repos.yaml: %w", err)
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("repos.yaml must hold one YAML document")
	}
	if len(root.Content) != 1 || root.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("repos.yaml must be a mapping")
	}
	doc := &Document{Defaults: map[string]any{}, Profiles: map[string]map[string]any{}}
	seen := map[string]bool{}
	top := root.Content[0]
	version := 0
	for i := 0; i+1 < len(top.Content); i += 2 {
		key, value := top.Content[i], top.Content[i+1]
		if !topKeys[key.Value] {
			return nil, fmt.Errorf("repos.yaml: unknown key %q (want %s)", key.Value, strings.Join(sortedKeys(topKeys), ", "))
		}
		if seen[key.Value] {
			return nil, fmt.Errorf("repos.yaml: duplicate key %q", key.Value)
		}
		seen[key.Value] = true
		switch key.Value {
		case "version":
			if err := value.Decode(&version); err != nil {
				return nil, fmt.Errorf("repos.yaml: version: %w", err)
			}
		case "settings":
			dec := yaml.NewDecoder(bytes.NewReader(nodeBytes(value)))
			dec.KnownFields(true)
			if err := dec.Decode(&doc.Settings); err != nil && !errors.Is(err, io.EOF) {
				return nil, fmt.Errorf("repos.yaml: settings: %w", err)
			}
		case "defaults":
			if err := decodeMap(value, &doc.Defaults); err != nil {
				return nil, fmt.Errorf("repos.yaml: defaults: %w", err)
			}
		case "profiles":
			var profiles map[string]map[string]any
			if err := value.Decode(&profiles); err != nil {
				return nil, fmt.Errorf("repos.yaml: profiles: %w", err)
			}
			for name, p := range profiles {
				if p == nil {
					p = map[string]any{}
				}
				doc.Profiles[name] = p
			}
		case "repos":
			if value.Kind != yaml.MappingNode {
				return nil, errors.New("repos.yaml: repos must be a mapping of owner/repo to its block")
			}
			names := map[string]bool{}
			for j := 0; j+1 < len(value.Content); j += 2 {
				name := value.Content[j].Value
				if !repoNameRE.MatchString(name) {
					return nil, fmt.Errorf("repos.yaml: %q is not an owner/repo name", name)
				}
				if names[name] {
					return nil, fmt.Errorf("repos.yaml: repository %q appears twice", name)
				}
				names[name] = true
				cfg := map[string]any{}
				if err := decodeMap(value.Content[j+1], &cfg); err != nil {
					return nil, fmt.Errorf("repos.yaml: %s: %w", name, err)
				}
				doc.Repos = append(doc.Repos, Entry{Name: name, Config: cfg})
			}
		}
	}
	if version != Version {
		return nil, fmt.Errorf("repos.yaml: version must be %d, got %d", Version, version)
	}
	if !seen["repos"] {
		return nil, errors.New("repos.yaml: missing repos")
	}
	return doc, nil
}

// Load reads and parses the file at path.
func Load(path string) (*Document, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// decodeMap decodes a mapping node, treating an empty or null block as {}.
func decodeMap(n *yaml.Node, out *map[string]any) error {
	if n.Kind == yaml.ScalarNode && n.Tag == "!!null" {
		return nil
	}
	if n.Kind != yaml.MappingNode {
		return errors.New("must be a mapping")
	}
	var m map[string]any
	if err := n.Decode(&m); err != nil {
		return err
	}
	if m != nil {
		*out = m
	}
	return nil
}

func nodeBytes(n *yaml.Node) []byte {
	b, err := yaml.Marshal(n)
	if err != nil {
		return nil
	}
	return b
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// Resolved is one repository after defaults and profiles, as a feature sees it.
type Resolved struct {
	Name string
	// Values holds the effective value of each enabled feature key.
	Values map[string]any
	// Settings are the watcher settings, for features that need them.
	Settings Settings
}

// Has reports whether a feature is enabled on this repository.
func (r *Resolved) Has(key string) bool { _, ok := r.Values[key]; return ok }

// Value returns the effective value of an enabled feature key, and whether the
// feature is enabled on this repository.
func (r *Resolved) Value(key string) (any, bool) { v, ok := r.Values[key]; return v, ok }

// Resolve applies, for each repository, its profiles (`use`) and its own block,
// then lays `defaults` under every feature that ended up enabled. A default
// supplies values; it never switches a feature on, except a plain value such as
// `provider: claude`, which every repository takes. A feature set to false is
// off. Unknown keys are an error that names the known ones: a typo must not
// disable a feature quietly.
func Resolve(doc *Document) ([]*Resolved, error) {
	var out []*Resolved
	var problems []string
	for _, entry := range doc.Repos {
		enabling := map[string]any{}
		for key, value := range doc.Defaults {
			switch value.(type) {
			case map[string]any, []any, bool, nil:
			default:
				enabling[key] = value
			}
		}
		for _, name := range useList(entry.Config["use"]) {
			p, ok := doc.Profiles[name]
			if !ok {
				problems = append(problems, fmt.Sprintf("%s: unknown profile %q (known: %s)", entry.Name, name, strings.Join(sortedKeys(doc.Profiles), ", ")))
				continue
			}
			for key, value := range p {
				enabling[key] = mergeValue(enabling[key], value)
			}
		}
		for key, value := range entry.Config {
			if key != "use" {
				enabling[key] = mergeValue(enabling[key], value)
			}
		}
		r := &Resolved{Name: entry.Name, Values: map[string]any{}, Settings: doc.Settings}
		for _, key := range sortedKeys(enabling) {
			if _, ok := registry[key]; !ok {
				problems = append(problems, fmt.Sprintf("%s: unknown key %q (known: %s)", entry.Name, key, strings.Join(FeatureKeys(), ", ")))
				continue
			}
			value := enabling[key]
			if value == nil || value == false {
				continue
			}
			if base, ok := doc.Defaults[key]; ok {
				switch base.(type) {
				case map[string]any, []any:
					value = mergeValue(base, value)
				}
			}
			r.Values[key] = value
		}
		out = append(out, r)
	}
	for name := range doc.Defaults {
		if name == "use" {
			problems = append(problems, "defaults: `use` belongs to a repository, not to defaults")
		} else if _, ok := registry[name]; !ok {
			problems = append(problems, fmt.Sprintf("defaults: unknown key %q (known: %s)", name, strings.Join(FeatureKeys(), ", ")))
		}
	}
	for name, p := range doc.Profiles {
		for key := range p {
			if _, ok := registry[key]; !ok {
				problems = append(problems, fmt.Sprintf("profile %s: unknown key %q (known: %s)", name, key, strings.Join(FeatureKeys(), ", ")))
			}
		}
	}
	if len(problems) > 0 {
		slices.Sort(problems)
		return nil, errors.New(strings.Join(problems, "\n"))
	}
	return out, nil
}

func useList(v any) []string {
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

// mergeValue lays own over base. Two mappings merge key by key, lists and
// scalars are replaced, `true` over a mapping keeps the inherited mapping (the
// feature is on with its defaults), and `false` or null switches it off. A list
// of blocks takes the inherited mapping under each of its items.
func mergeValue(base, own any) any {
	if own == nil || own == false {
		return own
	}
	if list, ok := own.([]any); ok {
		baseMap, _ := base.(map[string]any)
		merged := make([]any, 0, len(list))
		for _, item := range list {
			if item == true || item == nil {
				item = map[string]any{}
			}
			if m, ok := item.(map[string]any); ok && baseMap != nil {
				item = mergeValue(baseMap, m)
			}
			merged = append(merged, item)
		}
		return merged
	}
	if own == true {
		if m, ok := base.(map[string]any); ok {
			return copyMap(m)
		}
		if list, ok := base.([]any); ok {
			return list
		}
		return map[string]any{}
	}
	ownMap, ownIsMap := own.(map[string]any)
	baseMap, baseIsMap := base.(map[string]any)
	if ownIsMap && baseIsMap {
		out := copyMap(baseMap)
		for k, v := range ownMap {
			out[k] = mergeValue(out[k], v)
		}
		return out
	}
	if ownIsMap {
		return copyMap(ownMap)
	}
	return own
}

func copyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
