package repos

import (
	"bytes"
	"fmt"
	"maps"
	"slices"

	"gopkg.in/yaml.v3"
)

// Marshal writes the document as a repos.yaml: version, settings, defaults, profiles, then the
// repositories in their order. Keys are sorted and comments are gone; an empty setting is left
// out, and a feature that is only switched on is written as `true`.
func (d *Document) Marshal() ([]byte, error) {
	var settings map[string]any
	raw, err := yaml.Marshal(d.Settings)
	if err == nil {
		err = yaml.Unmarshal(raw, &settings)
	}
	if err != nil {
		return nil, err
	}
	reposNode := &yaml.Node{Kind: yaml.MappingNode}
	for _, e := range d.Repos {
		reposNode.Content = append(reposNode.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: e.Name}, featureNode(e.Config))
	}
	out := struct {
		Version  int                       `yaml:"version"`
		Settings map[string]any            `yaml:"settings,omitempty"`
		Defaults map[string]any            `yaml:"defaults,omitempty"`
		Profiles map[string]map[string]any `yaml:"profiles,omitempty"`
		Repos    *yaml.Node                `yaml:"repos"`
	}{Version, dropEmpty(settings), d.Defaults, d.Profiles, reposNode}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(out); err != nil {
		return nil, err
	}
	return buf.Bytes(), enc.Close()
}

// dropEmpty removes the zero values of a decoded mapping, depth first, so a settings block
// holds only what was set.
func dropEmpty(m map[string]any) map[string]any {
	for k, v := range m {
		if sub, ok := v.(map[string]any); ok {
			v = dropEmpty(sub)
			m[k] = v
		}
		switch x := v.(type) {
		case nil:
			delete(m, k)
		case string:
			if x == "" {
				delete(m, k)
			}
		case int:
			if x == 0 {
				delete(m, k)
			}
		case map[string]any:
			if len(x) == 0 {
				delete(m, k)
			}
		case []any:
			if len(x) == 0 {
				delete(m, k)
			}
		}
	}
	return m
}

// featureNode renders one repository's features. An empty mapping is a feature switched
// on with no options, which is written as `true`.
func featureNode(features map[string]any) *yaml.Node {
	m := &yaml.Node{Kind: yaml.MappingNode}
	keys := slices.Sorted(maps.Keys(features))
	for _, k := range keys {
		var v yaml.Node
		if block, ok := features[k].(map[string]any); ok && len(block) == 0 {
			_ = v.Encode(true)
		} else if err := v.Encode(features[k]); err != nil {
			_ = v.Encode(fmt.Sprint(features[k]))
		}
		m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: k}, &v)
	}
	return m
}
