package repos

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"

	"gopkg.in/yaml.v3"

	"opencode-dots/internal/store"
)

// This file is step 1 of the migration of ADR 0024: repos.yaml can be loaded into the
// database, the generated files can be built from the database, and the two can be
// compared. repos.yaml stays the source until a later step switches it.

// settingsJSON is how the watcher settings are stored: the keys of repos.yaml.
type settingsJSON struct {
	AwfURL    string            `json:"awf_url,omitempty"`
	StatePath string            `json:"state_path,omitempty"`
	MaxPerRun int               `json:"max_per_run,omitempty"`
	Executors map[string]string `json:"executors,omitempty"`
}

// StoreImport is what an import did.
type StoreImport struct {
	Added, Updated, Unchanged int
	Pruned                    []string
	// Kept lists projects the file omits but that stay, because work items name them.
	Kept []string
}

// ImportToStore loads a repos.yaml into a workspace of the database. The file is built
// first, with every feature checked, so a file that would not generate is never stored.
// Defaults and profiles are resolved into each project's features, as the generator
// sees them. It upserts by repository and deletes nothing unless prune is set, and then
// only projects without work items.
func ImportToStore(ctx context.Context, st *store.Store, workspace string, data []byte, prune bool) (StoreImport, error) {
	var res StoreImport
	doc, err := Parse(data)
	if err != nil {
		return res, err
	}
	// An empty directory, so the check does not read an awf-gh.yaml of the working directory.
	scratch, err := os.MkdirTemp("", "repos-import")
	if err != nil {
		return res, err
	}
	defer os.RemoveAll(scratch)
	if _, err := BuildDocument(doc, scratch, Settings{}); err != nil {
		return res, fmt.Errorf("repos.yaml does not generate, nothing imported: %w", err)
	}
	resolved, err := Resolve(doc)
	if err != nil {
		return res, err
	}
	raw, err := json.Marshal(settingsJSON{AwfURL: doc.Settings.AwfURL, StatePath: doc.Settings.StatePath, MaxPerRun: doc.Settings.MaxPerRun, Executors: doc.Settings.Executors})
	if err != nil {
		return res, err
	}
	if err := st.SetWorkspaceSettings(ctx, workspace, raw); err != nil {
		return res, err
	}
	inFile := map[string]bool{}
	for i, r := range resolved {
		inFile[r.Name] = true
		known, err := st.Projects(ctx, workspace, r.Name)
		if err != nil {
			return res, err
		}
		_, changed, err := st.UpsertProject(ctx, workspace, r.Name, i, r.Values)
		if err != nil {
			return res, fmt.Errorf("%s: %w", r.Name, err)
		}
		switch {
		case len(known) == 0:
			res.Added++
		case changed:
			res.Updated++
		default:
			res.Unchanged++
		}
	}
	if !prune {
		return res, nil
	}
	existing, err := st.Projects(ctx, workspace)
	if err != nil {
		return res, err
	}
	for _, p := range existing {
		if inFile[p.Repo] {
			continue
		}
		switch err := st.DeleteProject(ctx, p.ID); {
		case err == nil:
			res.Pruned = append(res.Pruned, p.Repo)
		case errors.Is(err, store.ErrInUse):
			res.Kept = append(res.Kept, p.Repo)
		default:
			return res, err
		}
	}
	return res, nil
}

// documentFromStore rebuilds a Document from what the database holds. Features are stored
// resolved, so there are no defaults and no profiles left to apply.
func documentFromStore(ctx context.Context, st *store.Store, workspace string) (*Document, error) {
	projects, err := st.Projects(ctx, workspace)
	if err != nil {
		return nil, err
	}
	raw, err := st.WorkspaceSettings(ctx, workspace)
	if err != nil {
		return nil, err
	}
	var s settingsJSON
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("workspace %s: stored settings are not JSON: %w", workspace, err)
	}
	doc := &Document{Settings: Settings{AwfURL: s.AwfURL, StatePath: s.StatePath, MaxPerRun: s.MaxPerRun, Executors: s.Executors}}
	for _, p := range projects {
		doc.Repos = append(doc.Repos, Entry{Name: p.Repo, Config: p.Features})
	}
	return doc, nil
}

// BuildFromStore is Build with the database as the source: the same four generated files,
// from the projects of a workspace.
func BuildFromStore(ctx context.Context, st *store.Store, workspace, dir string, override Settings) (map[string][]byte, error) {
	doc, err := documentFromStore(ctx, st, workspace)
	if err != nil {
		return nil, err
	}
	return BuildDocument(doc, dir, override)
}

// ExportYAML writes a repos.yaml from the database. Defaults and profiles are not rebuilt:
// each project carries its resolved features, which generate the same files.
func ExportYAML(ctx context.Context, st *store.Store, workspace string) ([]byte, error) {
	doc, err := documentFromStore(ctx, st, workspace)
	if err != nil {
		return nil, err
	}
	root := &yaml.Node{Kind: yaml.MappingNode}
	put := func(m *yaml.Node, key string, value *yaml.Node) {
		m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, value)
	}
	scalar := func(tag, v string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: v} }
	put(root, "version", scalar("!!int", "1"))
	if settings := settingsNode(doc.Settings); settings != nil {
		put(root, "settings", settings)
	}
	repos := &yaml.Node{Kind: yaml.MappingNode}
	for _, e := range doc.Repos {
		put(repos, e.Name, featureNode(e.Config))
	}
	put(root, "repos", repos)
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(root); err != nil {
		return nil, err
	}
	return buf.Bytes(), enc.Close()
}

func settingsNode(s Settings) *yaml.Node {
	m := map[string]any{}
	if s.AwfURL != "" {
		m["awf_url"] = s.AwfURL
	}
	if s.StatePath != "" {
		m["state_path"] = s.StatePath
	}
	if s.MaxPerRun > 0 {
		m["max_per_run"] = s.MaxPerRun
	}
	if len(s.Executors) > 0 {
		m["executors"] = s.Executors
	}
	if len(m) == 0 {
		return nil
	}
	var n yaml.Node
	if err := n.Encode(m); err != nil {
		return nil
	}
	return &n
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

// DiffStoreAndFile builds the generated files from the database and from the file and
// names the ones that differ. An empty result means the two sources agree: it is the
// check that lets the source switch from the file to the database.
func DiffStoreAndFile(ctx context.Context, st *store.Store, workspace string, data []byte, dir string) ([]string, error) {
	fromFile, err := Build(data, dir, Settings{})
	if err != nil {
		return nil, fmt.Errorf("repos.yaml: %w", err)
	}
	fromStore, err := BuildFromStore(ctx, st, workspace, dir, Settings{})
	if err != nil {
		return nil, fmt.Errorf("database: %w", err)
	}
	names := map[string]bool{}
	for n := range fromFile {
		names[n] = true
	}
	for n := range fromStore {
		names[n] = true
	}
	var differ []string
	for n := range names {
		if !bytes.Equal(fromFile[n], fromStore[n]) {
			differ = append(differ, n)
		}
	}
	slices.Sort(differ)
	return differ, nil
}
