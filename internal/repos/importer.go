package repos

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// importOrder is the order features appear under a repository in an imported
// repos.yaml. Registration order would put the watcher rules before the cheap
// scalar keys a reader looks at first.
var importOrder = []string{"checkout", "sync", "provider", "tend", "review", "reply", "fix_ci", "fix_issue"}

type awfGhFile struct {
	Settings `yaml:",inline"`
	Repos    []struct {
		Repo          string            `yaml:"repo"`
		Workflow      string            `yaml:"workflow"`
		Scope         string            `yaml:"scope"`
		On            []string          `yaml:"on"`
		IgnoreAuthors []string          `yaml:"ignore_authors"`
		OnlyAuthors   []string          `yaml:"only_authors"`
		Labels        []string          `yaml:"labels"`
		Inputs        map[string]string `yaml:"inputs"`
	} `yaml:"repos"`
}

// Import builds a repos.yaml from the three files it replaces. It is lossy only
// where those files carry no data: comments and YAML anchors do not survive, and
// repeated blocks become `defaults`. Read the result, then run `generate`: the
// importer's own test regenerates the files and compares them rule for rule.
func Import(dir string) ([]byte, error) {
	var gh awfGhFile
	raw, err := os.ReadFile(filepath.Join(dir, FileAwfGh))
	if err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(raw, &gh); err != nil {
		return nil, fmt.Errorf("%s: %w", FileAwfGh, err)
	}
	workflowKey := map[string]string{}
	for key, f := range registry {
		if w := f.Workflow(); w != "" {
			workflowKey[w] = key
		}
	}

	type block map[string]any
	repoOrder := []string{}
	blocks := map[string]map[string][]block{} // repo -> feature -> blocks
	providers := map[string][]string{}        // repo -> provider input of each providerAware rule, "" when absent
	touch := func(repo string) {
		if _, ok := blocks[repo]; !ok {
			blocks[repo] = map[string][]block{}
			repoOrder = append(repoOrder, repo)
		}
	}
	for i, r := range gh.Repos {
		key, ok := workflowKey[r.Workflow]
		if !ok {
			return nil, fmt.Errorf("%s: repos[%d] (%s): no feature handles workflow %q; register one (see internal/repos/features_builtin.go)", FileAwfGh, i, r.Repo, r.Workflow)
		}
		touch(r.Repo)
		w, isWatch := registry[key].(watch)
		if !isWatch {
			return nil, fmt.Errorf("%s: repos[%d]: workflow %q belongs to feature %q, which is not a watcher rule", FileAwfGh, i, r.Workflow, key)
		}
		b := block{}
		switch {
		case slices.Equal(r.On, []string{"issue-labeled"}) && len(r.Labels) == 1 && w.label:
			b["label"] = r.Labels[0]
		default:
			if len(r.On) > 0 && !slices.Equal(r.On, w.defaultOn) {
				b["on"] = stringsToAny(r.On)
			}
			if len(r.Labels) > 0 {
				b["labels"] = stringsToAny(r.Labels)
			}
		}
		if len(r.IgnoreAuthors) > 0 {
			b["ignore_authors"] = stringsToAny(r.IgnoreAuthors)
		}
		if len(r.OnlyAuthors) > 0 {
			b["only_authors"] = stringsToAny(r.OnlyAuthors)
		}
		if r.Scope != "" && r.Scope != "local" {
			b["scope"] = r.Scope
		}
		inputs := map[string]any{}
		for k, v := range r.Inputs {
			inputs[k] = v
		}
		if w.providerAware {
			p, _ := inputs["provider"].(string)
			providers[r.Repo] = append(providers[r.Repo], p)
			delete(inputs, "provider")
		}
		if len(inputs) > 0 {
			b["inputs"] = inputs
		}
		blocks[r.Repo][key] = append(blocks[r.Repo][key], b)
	}

	// A provider shared by every provider aware rule of a repository becomes the
	// repository's own `provider`; a mixed one stays in the rules' inputs.
	repoProvider := map[string]string{}
	for repo, list := range providers {
		same := list[0] != ""
		for _, p := range list {
			same = same && p == list[0]
		}
		if same {
			repoProvider[repo] = list[0]
			continue
		}
		for key, bs := range blocks[repo] {
			if registry[key].(watch).providerAware {
				for i, p := range list {
					if i < len(bs) && p != "" {
						in, _ := bs[i]["inputs"].(map[string]any)
						if in == nil {
							in = map[string]any{}
						}
						in["provider"] = p
						bs[i]["inputs"] = in
					}
				}
			}
		}
	}

	// defaults: the list option most blocks of a feature share.
	defaults := map[string]map[string]any{}
	for _, key := range importOrder {
		var all []block
		for _, repo := range repoOrder {
			all = append(all, blocks[repo][key]...)
		}
		if len(all) < 2 {
			continue
		}
		for _, opt := range []string{"ignore_authors", "only_authors"} {
			counts := map[string]int{}
			sample := map[string]any{}
			for _, b := range all {
				if v, ok := b[opt]; ok {
					j, _ := json.Marshal(v)
					counts[string(j)]++
					sample[string(j)] = v
				}
			}
			best, bestN := "", 1
			for k, n := range counts {
				if n > bestN || (n == bestN && n > 1 && k < best) {
					best, bestN = k, n
				}
			}
			if bestN >= 2 {
				if defaults[key] == nil {
					defaults[key] = map[string]any{}
				}
				defaults[key][opt] = sample[best]
			}
		}
	}
	for key, d := range defaults {
		for _, repo := range repoOrder {
			for _, b := range blocks[repo][key] {
				for opt, def := range d {
					switch v, ok := b[opt]; {
					case ok && reflect.DeepEqual(v, def):
						delete(b, opt)
					case !ok:
						b[opt] = []any{} // an empty list switches the default off
					}
				}
			}
		}
	}

	// tend.list and repos.map add repositories awf-gh never watched.
	tendOf := map[string]bool{}
	hasTend := map[string]bool{}
	if data, err := os.ReadFile(filepath.Join(dir, FileTend)); err == nil {
		for n, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 0 || strings.HasPrefix(fields[0], "#") {
				continue
			}
			if !repoNameRE.MatchString(fields[0]) {
				return nil, fmt.Errorf("%s:%d: %q is not an owner/repo name", FileTend, n+1, fields[0])
			}
			auto := len(fields) > 1 && fields[1] == "true"
			touch(fields[0])
			hasTend[fields[0]] = true
			tendOf[fields[0]] = auto
		}
	}
	checkouts := map[string]string{}
	if data, err := os.ReadFile(filepath.Join(dir, FileCheckout)); err == nil {
		for n, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			repo, path, ok := strings.Cut(line, "=")
			if !ok || !repoNameRE.MatchString(repo) {
				return nil, fmt.Errorf("%s:%d: want owner/repo=/path, got %q", FileCheckout, n+1, line)
			}
			touch(repo)
			checkouts[repo] = path
		}
	}

	syncs := map[string]string{}
	if data, err := os.ReadFile(filepath.Join(dir, FileSync)); err == nil {
		for n, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			repo, path, ok := strings.Cut(line, "=")
			if !ok || !repoNameRE.MatchString(repo) {
				return nil, fmt.Errorf("%s:%d: want owner/repo=/path, got %q", FileSync, n+1, line)
			}
			touch(repo)
			syncs[repo] = path
		}
	}

	// Build the document as ordered nodes.
	doc := &yaml.Node{Kind: yaml.MappingNode}
	put := func(m *yaml.Node, key string, v *yaml.Node) {
		m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, v)
	}
	put(doc, "version", valueNode(Version))
	if gh.MaxPerRun > 0 && gh.MaxPerRun != defaultMaxPerRun {
		put(doc, "settings", valueNode(map[string]any{"max_per_run": gh.MaxPerRun}))
	}
	if len(defaults) > 0 {
		d := &yaml.Node{Kind: yaml.MappingNode}
		for _, key := range importOrder {
			if v, ok := defaults[key]; ok {
				put(d, key, valueNode(v))
			}
		}
		put(doc, "defaults", d)
	}
	repos := &yaml.Node{Kind: yaml.MappingNode}
	for _, repo := range repoOrder {
		m := &yaml.Node{Kind: yaml.MappingNode}
		for _, key := range importOrder {
			switch key {
			case "checkout":
				if p, ok := checkouts[repo]; ok {
					put(m, key, valueNode(p))
				}
			case "sync":
				if p, ok := syncs[repo]; ok {
					if p == checkouts[repo] {
						put(m, key, valueNode(true))
					} else {
						put(m, key, valueNode(p))
					}
				}
			case "provider":
				if p, ok := repoProvider[repo]; ok && p != "" {
					put(m, key, valueNode(p))
				}
			case "tend":
				if hasTend[repo] {
					if tendOf[repo] {
						put(m, key, valueNode(map[string]any{"auto_merge": true}))
					} else {
						put(m, key, valueNode(true))
					}
				}
			default:
				bs := blocks[repo][key]
				switch {
				case len(bs) == 0:
				case len(bs) == 1 && len(bs[0]) == 0:
					put(m, key, valueNode(true))
				case len(bs) == 1:
					put(m, key, valueNode(map[string]any(bs[0])))
				default:
					items := make([]any, len(bs))
					for i, b := range bs {
						if len(b) == 0 {
							items[i] = true
						} else {
							items[i] = map[string]any(b)
						}
					}
					put(m, key, valueNode(items))
				}
			}
		}
		put(repos, repo, m)
	}
	put(doc, "repos", repos)

	var buf bytes.Buffer
	buf.WriteString("# Every repository the dot works on, and what it does on each.\n" +
		"# Source of truth: awf-gh.yaml, tend.list and repos.map are generated from it\n" +
		"# (`make repos`). `bin/repos features` lists the keys a repository accepts.\n" +
		"# Local file, gitignored: this repository is public, these names are not.\n")
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	if len(repoOrder) == 0 {
		return nil, errors.New("nothing to import: no repository in awf-gh.yaml, tend.list or repos.map")
	}
	return buf.Bytes(), nil
}

func stringsToAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

// valueNode encodes a value as a YAML node, flowing the short lists and the
// one-key blocks so an imported file stays one line per option.
func valueNode(v any) *yaml.Node {
	var n yaml.Node
	if err := n.Encode(v); err != nil {
		return &yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprint(v)}
	}
	flowShort(&n)
	return &n
}

func flowShort(n *yaml.Node) {
	switch n.Kind {
	case yaml.SequenceNode:
		scalars := true
		for _, c := range n.Content {
			flowShort(c)
			scalars = scalars && c.Kind == yaml.ScalarNode
		}
		if scalars {
			n.Style = yaml.FlowStyle
		}
	case yaml.MappingNode:
		keys := []string{}
		for i := 0; i < len(n.Content); i += 2 {
			flowShort(n.Content[i+1])
			keys = append(keys, n.Content[i].Value)
		}
		sort.Strings(keys)
		if len(keys) == 1 && n.Content[1].Kind == yaml.ScalarNode {
			n.Style = yaml.FlowStyle
		}
	}
}
