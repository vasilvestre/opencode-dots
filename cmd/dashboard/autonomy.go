package main

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// What bin/publish-gate left behind when the autonomy ceiling stopped a
// publication: notes/ (observe, class 3) and held/ (propose). Read only: nothing
// here writes, releases or posts.
const (
	autonomyNoteCap     = 16 << 10  // a gate file is six short lines
	autonomyArtifactCap = 256 << 10 // a prepared comment or patch
	autonomyListCap     = 500       // newest files per directory
)

// A gate file name: UTC timestamp, action, slug.
var autonomyNameRE = regexp.MustCompile(`^(\d{8}T\d{6}Z)-[A-Za-z0-9._-]+\.txt$`)

// gateItem is one file written by publish-gate.
type gateItem struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	At       string `json:"at"`
	Action   string `json:"action"`
	Ref      string `json:"ref"`
	Autonomy string `json:"autonomy"`
	Class    string `json:"class"`
	Artifact string `json:"artifact"`
	Reason   string `json:"reason"`
	Target   string `json:"target,omitempty"` // file-issue: the upstream OWNER/REPO
	Title    string `json:"title,omitempty"`
	Readable bool   `json:"readable"`
}

// parseGateFile reads the "key: value" lines of one gate file. Unknown keys and
// lines without a colon are ignored; a repeated key keeps its first value.
func parseGateFile(name, kind, text string) (gateItem, bool) {
	m := autonomyNameRE.FindStringSubmatch(name)
	if m == nil {
		return gateItem{}, false
	}
	it := gateItem{Name: name, Kind: kind}
	if t, err := time.Parse("20060102T150405Z", m[1]); err == nil {
		it.At = t.UTC().Format(time.RFC3339)
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(text, "\n") {
		key, val, ok := strings.Cut(line, ":")
		if !ok || seen[key] {
			continue
		}
		val = strings.TrimSpace(val)
		switch key {
		case "action":
			it.Action = val
		case "ref":
			it.Ref = val
		case "autonomy":
			it.Autonomy = val
		case "class":
			it.Class = val
		case "artifact":
			it.Artifact = val
		case "reason":
			it.Reason = val
		case "target":
			it.Target = val
		case "title":
			it.Title = val
		default:
			continue
		}
		seen[key] = true
	}
	if it.Artifact == "none" {
		it.Artifact = ""
	}
	return it, true
}

// within reports whether path is root or below it, by path elements.
func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// resolveArtifact maps the path a gate recorded to a regular file that really
// sits inside state. The gate runs in a container where state is /state, so a
// path under that prefix is tried against the dashboard's state too. Symlinks are
// resolved before the check, so one pointing out of the tree is refused.
func resolveArtifact(state, recorded string) (string, bool) {
	if recorded == "" || strings.ContainsRune(recorded, 0) {
		return "", false
	}
	for _, p := range strings.Split(filepath.ToSlash(recorded), "/") {
		if p == ".." {
			return "", false
		}
	}
	root, err := filepath.EvalSymlinks(state)
	if err != nil {
		return "", false
	}
	var cands []string
	if !filepath.IsAbs(recorded) {
		cands = append(cands, filepath.Join(state, recorded))
	} else {
		cands = append(cands, recorded)
		if rest, ok := strings.CutPrefix(recorded, "/state/"); ok {
			cands = append(cands, filepath.Join(state, rest))
		}
	}
	for _, c := range cands {
		real, err := filepath.EvalSymlinks(filepath.Clean(c))
		if err != nil || !within(root, real) {
			continue
		}
		if info, err := os.Stat(real); err == nil && info.Mode().IsRegular() {
			return real, true
		}
	}
	return "", false
}

// resolveReleasedArtifact is resolveArtifact for a released record. publish-held
// moves the artifact copy from held/artifacts/ to held/released/artifacts/ and
// leaves the record as it was, so a path under held/artifacts/ that is gone is
// tried again under held/released/artifacts/.
func resolveReleasedArtifact(state, recorded string) (string, bool) {
	if real, ok := resolveArtifact(state, recorded); ok {
		return real, true
	}
	const from, to = "held/artifacts/", "held/released/artifacts/"
	if i := strings.Index(recorded, from); i >= 0 {
		return resolveArtifact(state, recorded[:i]+to+recorded[i+len(from):])
	}
	return "", false
}

// readCapped reads at most limit bytes of a regular file, refusing a symlink.
func readCapped(path string, limit int64) (string, bool) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit))
	if err != nil {
		return "", false
	}
	return string(data), true
}

// gateItems lists one directory, newest first.
func (d *dash) gateItems(kind string) []gateItem {
	dir := filepath.Join(d.state, kind)
	out := []gateItem{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	var names []string
	for _, e := range entries {
		if autonomyNameRE.MatchString(e.Name()) {
			names = append(names, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	if len(names) > autonomyListCap {
		names = names[:autonomyListCap]
	}
	for _, n := range names {
		text, ok := readCapped(filepath.Join(dir, n), autonomyNoteCap)
		if !ok {
			continue
		}
		it, ok := parseGateFile(n, kind, text)
		if !ok {
			continue
		}
		_, it.Readable = resolveArtifact(d.state, it.Artifact)
		out = append(out, it)
	}
	return out
}

// autonomy lists what the ceiling stopped.
func (d *dash) autonomy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || !d.trustedHost(r.Host) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	writeJSON(w, map[string][]gateItem{
		"notes":    d.gateItems("notes"),
		"held":     d.gateItems("held"),
		"released": d.releasedItems(),
	})
}

// autonomyArtifact serves the artifact one gate file points at, as text. The
// caller names the gate file (kind and name), never a path: the path comes from
// the file and must resolve inside state.
func (d *dash) autonomyArtifact(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || !d.trustedHost(r.Host) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	kind, name := r.URL.Query().Get("kind"), r.URL.Query().Get("name")
	if (kind != "notes" && kind != "held" && kind != "released") || !autonomyNameRE.MatchString(name) {
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "bad kind or name"})
		return
	}
	dir := kind
	if kind == "released" {
		dir = filepath.Join("held", "released")
	}
	text, ok := readCapped(filepath.Join(d.state, dir, name), autonomyNoteCap)
	if !ok {
		writeJSONStatus(w, http.StatusNotFound, map[string]string{"error": "no such item"})
		return
	}
	it, _ := parseGateFile(name, kind, text)
	real, ok := resolveArtifact(d.state, it.Artifact)
	if !ok && kind == "released" {
		real, ok = resolveReleasedArtifact(d.state, it.Artifact)
	}
	if !ok {
		writeJSONStatus(w, http.StatusNotFound, map[string]string{"error": "artifact is missing or outside the state directory"})
		return
	}
	if info, err := os.Stat(real); err != nil || info.Size() > autonomyArtifactCap {
		writeJSONStatus(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "artifact is larger than the 256 KiB cap"})
		return
	}
	body, ok := readCapped(real, autonomyArtifactCap)
	if !ok {
		writeJSONStatus(w, http.StatusNotFound, map[string]string{"error": "artifact cannot be read"})
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	io.WriteString(w, body)
}
