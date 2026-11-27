package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"opencode-dots/internal/repos"
)

func (d *dash) file(w http.ResponseWriter, r *http.Request) {
	rel := filepath.Clean("/" + r.URL.Query().Get("path"))
	parts := strings.Split(strings.TrimPrefix(rel, "/"), "/")
	if len(parts) < 2 || !artifactDirs[parts[0]] {
		http.Error(w, "refused", http.StatusNotFound)
		return
	}
	ctype, ok := artifactExt[filepath.Ext(rel)]
	if !ok {
		http.Error(w, "refused", http.StatusNotFound)
		return
	}
	path := filepath.Join(d.state, rel)
	if !strings.HasPrefix(path, filepath.Join(d.state)+string(os.PathSeparator)) {
		http.Error(w, "refused", http.StatusNotFound)
		return
	}
	serveFile(w, r, path, ctype)
}

// --- Editable files -------------------------------------------------------
//
// The dashboard may read and replace the workflow definitions and the local
// watcher configs. It writes files, never git: the repository is the history.
// Every path goes through resolveEditable, and every write proves it came from
// this page on this host.

const maxEditSize = 1 << 20

var workflowFileRE = regexp.MustCompile(`^[A-Za-z0-9_-]+\.yaml$`)

// localFiles are the configs that live in the repository root. repos.yaml is the
// one to edit; the others are generated from it (see internal/repos) and stay
// readable here, but a save of one is refused while repos.yaml exists.
var localFiles = []struct {
	Path, Label, Kind string
	Generated         bool
	// Optional marks a generated file that exists only when something needs it: the
	// watcher file when a repository has a watch rule (ADR 0030), sync.map when one syncs.
	// Its absence is not a problem.
	Optional bool
}{
	{"repos.yaml", "repos.yaml", "repos", false, false},
	{"awf-gh.yaml", "awf-gh.yaml", "watcher", true, true},
	{"tend.list", "tend.list", "list", true, false},
	{"repos.map", "repos.map", "map", true, false},
	{"sync.map", "sync.map", "map", true, true},
}

// generatedFile says the file is written by `bin/repos` from repos.yaml, so a
// hand edit would be overwritten at the next save of repos.yaml.
func generatedFile(rel string) bool {
	if repos.IsWatcherFile(rel) {
		return true // awf-gh.<executor>.yaml too (ADR 0031)
	}
	for _, f := range localFiles {
		if f.Path == rel {
			return f.Generated
		}
	}
	return false
}

type editableFile struct {
	Path  string `json:"path"`
	Label string `json:"label"`
	Kind  string `json:"kind"`
	// Generated marks a file written from repos.yaml: read it, edit repos.yaml.
	Generated bool `json:"generated,omitempty"`
	// Optional is a generated file that is only written when needed: not being there is fine.
	Optional bool   `json:"optional,omitempty"`
	Exists   bool   `json:"exists"`
	Size     int64  `json:"size"`
	ModTime  string `json:"mod_time"`
}

// resolveEditable maps a page-supplied path to an absolute path inside the
// repository. It returns false for anything outside workflows/ and the three
// known local files; no traversal, no symlinked extension, no hidden files.
func (d *dash) resolveEditable(rel string) (string, bool) {
	// Reject traversal before cleaning: filepath.Clean would fold
	// "../awf-gh.yaml" back into a valid name.
	if rel == "" || filepath.IsAbs(rel) || strings.Contains(rel, `\`) {
		return "", false
	}
	for _, seg := range strings.Split(rel, "/") {
		if seg == ".." {
			return "", false
		}
	}
	rel = strings.TrimPrefix(filepath.Clean(rel), "./")
	for _, f := range localFiles {
		if rel == f.Path {
			return filepath.Join(d.root, rel), true
		}
	}
	if repos.IsWatcherFile(rel) {
		return filepath.Join(d.root, rel), true
	}
	if name, ok := strings.CutPrefix(rel, "workflows/"); ok && workflowFileRE.MatchString(name) {
		return filepath.Join(d.root, "workflows", name), true
	}
	return "", false
}

func (d *dash) files(w http.ResponseWriter, r *http.Request) {
	out := []editableFile{}
	for _, name := range listFiles(filepath.Join(d.root, "workflows"), ".yaml") {
		rel := "workflows/" + name
		out = append(out, editableFile{Path: rel, Label: strings.TrimSuffix(name, ".yaml"), Kind: "workflow"})
	}
	for _, f := range localFiles {
		out = append(out, editableFile{Path: f.Path, Label: f.Label, Kind: f.Kind, Generated: f.Generated, Optional: f.Optional})
	}
	// One watcher file per executor, when the generator wrote any.
	if entries, err := os.ReadDir(d.root); err == nil {
		for _, e := range entries {
			if name := e.Name(); name != repos.FileAwfGh && repos.IsWatcherFile(name) {
				out = append(out, editableFile{Path: name, Label: name, Kind: "watcher", Generated: true})
			}
		}
	}
	for i := range out {
		abs, _ := d.resolveEditable(out[i].Path)
		if info, err := os.Stat(abs); err == nil && !info.IsDir() {
			out[i].Exists = true
			out[i].Size = info.Size()
			out[i].ModTime = info.ModTime().UTC().Format(time.RFC3339)
		}
	}
	writeJSON(w, out)
}

func (d *dash) source(w http.ResponseWriter, r *http.Request) {
	abs, ok := d.resolveEditable(r.URL.Query().Get("path"))
	if !ok {
		http.Error(w, "refused", http.StatusForbidden)
		return
	}
	serveFile(w, r, abs, "text/plain; charset=utf-8")
}

func (d *dash) saveFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// A cross-site form can POST text/plain, so require JSON and a loopback
	// origin: the first needs a preflight no foreign page can pass, the second
	// blocks a rebinding host that resolves to 127.0.0.1.
	if !d.trustedRequest(r) {
		http.Error(w, "refused origin", http.StatusForbidden)
		return
	}
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		http.Error(w, "expected application/json", http.StatusUnsupportedMediaType)
		return
	}
	var req struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxEditSize+4096)).Decode(&req); err != nil {
		http.Error(w, "bad body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if len(req.Content) > maxEditSize {
		http.Error(w, "file too large", http.StatusRequestEntityTooLarge)
		return
	}
	abs, ok := d.resolveEditable(req.Path)
	if !ok {
		http.Error(w, "refused path", http.StatusForbidden)
		return
	}
	rel := strings.TrimPrefix(filepath.Clean(req.Path), "./")
	auditTarget(r, rel)
	if generatedFile(rel) {
		if _, err := os.Stat(filepath.Join(d.root, repos.SourceFile)); err == nil {
			writeJSONStatus(w, http.StatusConflict, map[string]string{"error": rel + " is generated from repos.yaml: edit repos.yaml and save it"})
			return
		}
	}
	// The lock covers the build too: it reads the awf-gh.yaml an earlier save
	// may be replacing.
	d.editMu.Lock()
	defer d.editMu.Unlock()
	// repos.yaml must generate before it is written: a typo in a feature key or
	// an unknown event would otherwise stop the next polling pass.
	var generated map[string][]byte
	if rel == repos.SourceFile {
		var err error
		if generated, err = repos.Build([]byte(req.Content), d.root, repos.Settings{}); err != nil {
			writeJSONStatus(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
			return
		}
	}
	// Refuse YAML that does not parse: a broken watcher config or workflow
	// would stop the next polling pass, and the page cannot tell the user when
	// awf-gh runs. The edit stays in the textarea; nothing hits the disk.
	if strings.HasSuffix(abs, ".yaml") {
		var probe any
		if err := yaml.Unmarshal([]byte(req.Content), &probe); err != nil {
			writeJSONStatus(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
			return
		}
	}
	backup, err := backupFile(abs)
	if err != nil {
		http.Error(w, "backup failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := atomicWrite(abs, []byte(req.Content)); err != nil {
		http.Error(w, "write failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	resp := map[string]any{"path": req.Path, "bytes": len(req.Content), "backup": backup}
	if generated != nil {
		// The source is saved; the files the other tools read follow it. They
		// are rewritten only when their content changed, and never half written.
		changed, err := repos.Write(d.root, generated)
		if err != nil {
			writeJSONStatus(w, http.StatusInternalServerError, map[string]string{
				"error": "repos.yaml saved, generated files not refreshed (already rewritten: [" + strings.Join(changed, " ") + "]); save again to finish: " + err.Error(),
			})
			return
		}
		resp["generated"] = changed
	}
	writeJSON(w, resp)
}

// backupFile copies the previous content beside the original, stamped, and
// keeps the last 20 per file. A first write has nothing to copy.
func backupFile(abs string) (string, error) {
	info, err := os.Stat(abs)
	if err != nil || info.IsDir() {
		return "", nil
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	name := filepath.Base(abs) + "." + time.Now().Format("20060102-150405") + ".bak"
	path := filepath.Join(filepath.Dir(abs), name)
	if err := os.WriteFile(path, data, info.Mode().Perm()); err != nil {
		return "", err
	}
	pruneBackups(abs, 20)
	return name, nil
}

func pruneBackups(abs string, keep int) {
	matches, _ := filepath.Glob(abs + ".*.bak")
	slices.Sort(matches)
	for len(matches) > keep {
		os.Remove(matches[0])
		matches = matches[1:]
	}
}

func atomicWrite(abs string, data []byte) error {
	perm := os.FileMode(0o644)
	if info, err := os.Stat(abs); err == nil {
		perm = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(abs), ".save-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, perm); err != nil {
		return err
	}
	return os.Rename(name, abs)
}
