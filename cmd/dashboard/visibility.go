package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// What a signed-in visitor may see (ADR 0044). A visitor is any request that is not the host's
// own user on loopback (ownerRequest). The session holds the repositories the visitor's own
// GitHub token showed them to reach at sign-in (rights.go); a lookup in that set is the whole check.
//
// The wrappers below sit in front of the handlers, so a handler knows nothing of visitors:
// scoped filters what a read returns, ownerOnly refuses the rest. With sign-in off (d.auth is
// nil) both return the handler untouched.

const auditReadEvery = time.Minute

// viewer is one authenticated visitor: the login and the repositories its session may see.
type viewer struct {
	login string
	set   map[string]bool
}

func (v *viewer) can(repo string) bool {
	return repo != "" && v.set[strings.ToLower(repo)]
}

// who classifies a request. It answers 401 to a visitor who has not signed in and 403 to a
// request from a host the dashboard does not trust; ok is false once it has answered.
func (d *dash) who(w http.ResponseWriter, r *http.Request) (v *viewer, owner, ok bool) {
	if ownerRequest(r) {
		return nil, true, true
	}
	if !d.trustedHost(r.Host) {
		authError(w, http.StatusForbidden, "forbidden")
		return nil, false, false
	}
	login, set := d.sessionView(r)
	if login == "" {
		writeJSONStatus(w, http.StatusUnauthorized, map[string]any{"error": "sign in with GitHub", "signin": true})
		return nil, false, false
	}
	return &viewer{login: login, set: set}, false, true
}

// gateFunc decides, before the handler runs, whether a visitor may read what the request names.
// It returns the repository for the audit row.
type gateFunc func(d *dash, v *viewer, r *http.Request) (repo string, ok bool)

// shapeFunc rewrites the JSON a handler answered to the repositories a visitor may see.
type shapeFunc func(v *viewer, r *http.Request, body []byte) ([]byte, error)

// rule says how a read endpoint treats a visitor. gate refuses before the handler runs, shape
// filters its answer, prep adjusts the request the handler sees (a visitor-only change).
type rule struct {
	gate  gateFunc
	shape shapeFunc
	prep  func(r *http.Request) *http.Request
}

// scoped serves a read endpoint to the owner unchanged and to a signed-in visitor through rule.
func scoped(d *dash, h http.HandlerFunc, rl rule) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.auth == nil {
			h(w, r)
			return
		}
		v, owner, ok := d.who(w, r)
		if !ok {
			return
		}
		if owner {
			h(w, r)
			return
		}
		target := ""
		if rl.gate != nil {
			repo, ok := rl.gate(d, v, r)
			if !ok { // the same answer as a record that does not exist
				d.recordRead(r, v.login, "", http.StatusNotFound)
				writeJSONStatus(w, http.StatusNotFound, map[string]string{"error": "not found"})
				return
			}
			target = repo
		}
		hr := r
		if rl.prep != nil {
			hr = rl.prep(r)
		}
		if rl.shape == nil {
			sw := &statusWriter{ResponseWriter: w}
			h(sw, hr)
			d.recordRead(r, v.login, target, sw.status)
			return
		}
		rec := &bufferedResponse{header: http.Header{}}
		h(rec, hr)
		body := rec.buf.Bytes()
		if rec.status == 0 || rec.status == http.StatusOK {
			shaped, err := rl.shape(v, r, body)
			if err != nil {
				log.Printf("visitor filter: %s: %v", r.URL.Path, err)
				d.recordRead(r, v.login, target, http.StatusInternalServerError)
				authError(w, http.StatusInternalServerError, "cannot filter the answer")
				return
			}
			body = shaped
		}
		for k, vals := range rec.header {
			if k != "Content-Length" {
				w.Header()[k] = vals
			}
		}
		status := rec.status
		if status == 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
		w.Write(body)
		d.recordRead(r, v.login, target, status)
	}
}

// ownerOnly serves an endpoint to the owner alone. A signed-in visitor gets 403, one who has not
// signed in gets 401.
func (d *dash) ownerOnly(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.auth == nil {
			h(w, r)
			return
		}
		v, owner, ok := d.who(w, r)
		if !ok {
			return
		}
		if owner {
			h(w, r)
			return
		}
		// A write is recorded by audited; a read refused here is recorded now.
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			d.recordRead(r, v.login, "", http.StatusForbidden)
		}
		writeJSONStatus(w, http.StatusForbidden, map[string]string{"error": "owner only"})
	}
}

// bufferedResponse holds a handler's answer so it can be filtered before it is sent.
type bufferedResponse struct {
	header http.Header
	status int
	buf    bytes.Buffer
}

func (b *bufferedResponse) Header() http.Header         { return b.header }
func (b *bufferedResponse) Write(p []byte) (int, error) { return b.buf.Write(p) }
func (b *bufferedResponse) WriteHeader(code int) {
	if b.status == 0 {
		b.status = code
	}
}

// recordRead appends a row for a visitor's read, with the GitHub login as the caller. A page
// polls, so one row is kept per login, path and target a minute.
func (d *dash) recordRead(r *http.Request, login, target string, status int) {
	key := login + "|" + r.URL.Path + "|" + target + "|" + strconv.Itoa(status)
	if d.auth.SeenRecently(key, auditReadEvery) {
		return
	}
	d.appendAudit(r.Method+" "+r.URL.Path, target, login, status)
}

// recordSignIn is the row of a completed sign-in.
func (d *dash) recordSignIn(r *http.Request, login string) {
	d.appendAudit("POST /auth/device/poll", "", login, http.StatusOK)
}

// --- gates -------------------------------------------------------------------------------

// gateRun admits a run whose repository the visitor may see. A run with no known repository
// (not yet read, or one that names none) is hidden: fail closed.
func gateRun(d *dash, v *viewer, r *http.Request) (string, bool) {
	repo := d.target(r.URL.Query().Get("id"))["repo"]
	return repo, v.can(repo)
}

// gateThread admits the Ask conversation of a repository the visitor may see.
func gateThread(d *dash, v *viewer, r *http.Request) (string, bool) {
	repo := r.URL.Query().Get("repo")
	return repo, repoNameRE.MatchString(repo) && v.can(repo)
}

// gateFile admits an artifact file of a review or a reply whose repository the visitor may see.
func gateFile(d *dash, v *viewer, r *http.Request) (string, bool) {
	rel := filepath.Clean("/" + r.URL.Query().Get("path"))
	parts := strings.Split(strings.TrimPrefix(rel, "/"), "/")
	if len(parts) < 2 || !artifactDirs[parts[0]] {
		return "", false
	}
	var repo string
	switch parts[0] {
	case "reviews":
		repo = strings.TrimSpace(readFile(filepath.Join(d.state, "reviews", parts[1], "repo"), 200))
	case "replies":
		var pr struct {
			URL string `json:"url"`
		}
		loadJSON(filepath.Join(d.state, "replies", parts[1], "pr.json"), &pr)
		repo = repoFromURL(pr.URL)
	}
	return repo, v.can(repo)
}

// gateAutonomyArtifact admits the artifact of a gate file whose reference names a repository the
// visitor may see.
func gateAutonomyArtifact(d *dash, v *viewer, r *http.Request) (string, bool) {
	kind, name := r.URL.Query().Get("kind"), r.URL.Query().Get("name")
	if (kind != "notes" && kind != "held" && kind != "released") || !autonomyNameRE.MatchString(name) {
		return "", false
	}
	dir := kind
	if kind == "released" {
		dir = filepath.Join("held", "released")
	}
	text, ok := readCapped(filepath.Join(d.state, dir, name), autonomyNoteCap)
	if !ok {
		return "", false
	}
	it, _ := parseGateFile(name, kind, text)
	repo := repoOfRef(it.Ref)
	return repo, v.can(repo)
}

// --- shapes ------------------------------------------------------------------------------

// repoOfRef is the repository in a reference such as "owner/repo#12" or "owner/repo set-key".
func repoOfRef(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "# \t"); i >= 0 {
		s = s[:i]
	}
	if !repoNameRE.MatchString(s) {
		return ""
	}
	return s
}

func encodeJSON(v any) ([]byte, error) {
	var b bytes.Buffer
	err := json.NewEncoder(&b).Encode(v)
	return b.Bytes(), err
}

// decodeAny reads JSON keeping numbers as they were written.
func decodeAny(body []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	return dec.Decode(v)
}

func field(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

// listBy keeps the elements of a JSON array that keep accepts.
func listBy(keep func(v *viewer, m map[string]any) bool) shapeFunc {
	return func(v *viewer, r *http.Request, body []byte) ([]byte, error) {
		var in []map[string]any
		if err := decodeAny(body, &in); err != nil {
			return nil, err
		}
		out := []map[string]any{}
		for _, m := range in {
			if keep(v, m) {
				out = append(out, m)
			}
		}
		return encodeJSON(out)
	}
}

func byField(key string) shapeFunc {
	return listBy(func(v *viewer, m map[string]any) bool { return v.can(field(m, key)) })
}

func shapeRepoNames(v *viewer, r *http.Request, body []byte) ([]byte, error) {
	var in []string
	if err := decodeAny(body, &in); err != nil {
		return nil, err
	}
	out := []string{}
	for _, s := range in {
		if v.can(s) {
			out = append(out, s)
		}
	}
	return encodeJSON(out)
}

func shapeArtifacts(v *viewer, r *http.Request, body []byte) ([]byte, error) {
	var in artifacts
	if err := decodeAny(body, &in); err != nil {
		return nil, err
	}
	out := artifacts{Reviews: []review{}, Replies: []reply{}, Seen: []seen{}}
	for _, x := range in.Reviews {
		if v.can(x.Repo) {
			out.Reviews = append(out.Reviews, x)
		}
	}
	for _, x := range in.Replies {
		if v.can(x.Repo) {
			out.Replies = append(out.Replies, x)
		}
	}
	for _, x := range in.Seen {
		if v.can(x.Repo) {
			out.Seen = append(out.Seen, x)
		}
	}
	return encodeJSON(out)
}

// shapeUsage keeps the groups of visible repositories and adds the totals up again from them, so
// a hidden repository is in no count and no sum.
func shapeUsage(v *viewer, r *http.Request, body []byte) ([]byte, error) {
	var in struct {
		Groups []usageOne `json:"groups"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, err
	}
	groups := []usageOne{}
	var total float64
	var runs, unpriced int
	for _, g := range in.Groups {
		if !v.can(g.Repo) {
			continue
		}
		groups = append(groups, g)
		total += g.Cost
		runs += g.Runs
		unpriced += g.Unpriced
	}
	return encodeJSON(map[string]any{"total": total, "runs": runs, "unpriced": unpriced, "groups": groups})
}

// shapeRemote keeps the executors' runs of visible repositories. A run with no priced target is
// hidden, and so is the list of executors: the page of a visitor does not need them.
func shapeRemote(v *viewer, r *http.Request, body []byte) ([]byte, error) {
	var in struct {
		Executions []map[string]any `json:"executions"`
		Entries    []map[string]any `json:"entries"`
	}
	if err := decodeAny(body, &in); err != nil {
		return nil, err
	}
	out := struct {
		Executors  []remoteSnapshot `json:"executors"`
		Executions []map[string]any `json:"executions"`
		Entries    []map[string]any `json:"entries"`
	}{[]remoteSnapshot{}, []map[string]any{}, []map[string]any{}}
	visible := map[string]bool{}
	for _, e := range in.Entries {
		if v.can(repoOfRef(field(e, "target"))) {
			out.Entries = append(out.Entries, e)
			visible[field(e, "execution_id")] = true
		}
	}
	for _, e := range in.Executions {
		if visible[field(e, "execution_id")] {
			out.Executions = append(out.Executions, e)
		}
	}
	return encodeJSON(out)
}

// shapeAutonomy keeps the gate files whose reference names a visible repository.
func shapeAutonomy(v *viewer, r *http.Request, body []byte) ([]byte, error) {
	var in map[string][]gateItem
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, err
	}
	out := map[string][]gateItem{"notes": {}, "held": {}, "released": {}}
	for kind, items := range in {
		for _, it := range items {
			if v.can(repoOfRef(it.Ref)) {
				out[kind] = append(out[kind], it)
			}
		}
	}
	return encodeJSON(out)
}

// shapeLinks keeps a proposal only when the visitor may see both of its items: it carries the
// title of each.
func shapeLinks(v *viewer, r *http.Request, body []byte) ([]byte, error) {
	return listBy(func(v *viewer, m map[string]any) bool {
		a, _ := m["a"].(map[string]any)
		b, _ := m["b"].(map[string]any)
		return v.can(field(a, "repo")) && v.can(field(b, "repo"))
	})(v, r, body)
}

// shapeAudit keeps the rows whose target names a visible repository. The handler is asked for
// the most rows it gives, and the visitor gets as many of the visible ones as they asked for.
func shapeAudit(v *viewer, r *http.Request, body []byte) ([]byte, error) {
	var in []map[string]any
	if err := decodeAny(body, &in); err != nil {
		return nil, err
	}
	limit := 100
	if n := atoiLimit(r.URL.Query().Get("limit")); n > 0 && n <= 500 {
		limit = n
	}
	out := []map[string]any{}
	for _, m := range in {
		if len(out) < limit && v.can(repoOfRef(field(m, "target"))) {
			out = append(out, m)
		}
	}
	return encodeJSON(out)
}

func widenAudit(r *http.Request) *http.Request {
	r2 := r.Clone(context.Background())
	q := r2.URL.Query()
	q.Set("limit", "500")
	r2.URL.RawQuery = q.Encode()
	return r2
}

// atoiLimit reads a limit query value, 0 when it is absent or not a number.
func atoiLimit(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

var ruleRun = rule{gate: gateRun}
