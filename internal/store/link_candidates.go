package store

import (
	"context"
	"encoding/json"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode"
)

// Candidate pairs for a link proposal (ADR 0041). The pairs are chosen here, by rules, and
// never by a model: two open work items of the same workspace and the same trust level
// (ADR 0032) that share at least one token taken from a STRUCTURED field of their payloads.
// The title and any other free text are never read for a reference (ADR 0023). A model only
// judges the pairs this function returns.

// Limits of a candidate run.
const (
	DefaultCandidates = 5
	MaxCandidates     = 50
	maxTokenLen       = 200
	maxTokensPerItem  = 100
)

var (
	advisoryIDRE = regexp.MustCompile(`(?i)^(GHSA(-[23456789cfghjmpqrvwx]{4}){3}|CVE-[0-9]{4}-[0-9]{4,})$`)
	packageRE    = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*(/[a-z0-9][a-z0-9._-]*)?$`)
)

// LinkTokens returns the tokens a payload names in structured fields, sorted and without
// duplicates: `package:<name>`, `advisory:<GHSA or CVE id>` and `file:<path>`. The keys read
// are `package`, `packages`, `id`, `cves`, `advisory`, `file`, `files`, `path` and `paths`; a
// list entry is a string or an object with a `name` (packages) or `path` (files). Nothing is
// taken from any other key, and a value that is not shaped like its class is dropped.
func LinkTokens(payload json.RawMessage) []string {
	var doc map[string]any
	if json.Unmarshal(payload, &doc) != nil {
		return nil
	}
	set := map[string]bool{}
	add := func(class, v string) {
		if v != "" && len(set) < maxTokensPerItem {
			set[class+":"+v] = true
		}
	}
	for _, k := range []string{"package", "packages"} {
		for _, v := range listOf(doc[k], "name") {
			if v = strings.ToLower(strings.TrimSpace(v)); len(v) <= maxTokenLen && packageRE.MatchString(v) {
				add("package", v)
			}
		}
	}
	for _, k := range []string{"id", "cves", "advisory"} {
		for _, v := range listOf(doc[k], "id") {
			if v = strings.ToUpper(strings.TrimSpace(v)); advisoryIDRE.MatchString(v) {
				add("advisory", v)
			}
		}
	}
	for _, k := range []string{"file", "files", "path", "paths"} {
		for _, v := range listOf(doc[k], "path") {
			if v = cleanPath(v); v != "" {
				add("file", v)
			}
		}
	}
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for t := range set {
		out = append(out, t)
	}
	slices.Sort(out)
	return out
}

// listOf reads a string, a list of strings, or a list of objects holding the string under
// key. Anything else gives nothing.
func listOf(v any, key string) []string {
	switch x := v.(type) {
	case string:
		return []string{x}
	case []any:
		var out []string
		for _, e := range x {
			switch y := e.(type) {
			case string:
				out = append(out, y)
			case map[string]any:
				if s, ok := y[key].(string); ok {
					out = append(out, s)
				}
			}
		}
		return out
	}
	return nil
}

// cleanPath keeps a relative path with no control character and no parent step.
func cleanPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" || len(p) > maxTokenLen || strings.ContainsFunc(p, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }) {
		return ""
	}
	p = path.Clean(strings.TrimPrefix(p, "./"))
	if p == "." || p == ".." || strings.HasPrefix(p, "../") || strings.HasPrefix(p, "/") {
		return ""
	}
	return p
}

// CandidateItem is one side of a candidate pair.
type CandidateItem struct {
	ID      string
	Title   string
	Repo    string
	Trust   string
	Payload json.RawMessage
}

// LinkCandidate is a pair worth asking a model about, with the tokens it shares. A is the
// older work item: it would keep the references.
type LinkCandidate struct {
	A, B   CandidateItem
	Shared []string
}

// LinkCandidates lists at most max pairs (DefaultCandidates when max <= 0, MaxCandidates at
// most) of open work items of the workspace that share a structured token, in the same trust
// level, with no proposal between them in any state: an open one is pending, a decided one
// (declined, accepted, split) is a person's answer that a rerun must not ask again. The
// second result counts the distinct pairs that share a token but sit under different trust
// levels and were left out. The order is stable: most shared tokens first, then by id.
func (s *Store) LinkCandidates(ctx context.Context, workspace string, max int) ([]LinkCandidate, int, error) {
	switch {
	case max <= 0:
		max = DefaultCandidates
	case max > MaxCandidates:
		max = MaxCandidates
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT w.id, w.title, w.payload, p.repo, p.features FROM work_item w JOIN project p ON p.id = w.project_id
		WHERE p.workspace = ? AND w.state = 'open' ORDER BY w.created_at, w.id`, workspace)
	if err != nil {
		return nil, 0, err
	}
	var items []CandidateItem
	tokens := map[string][]int{} // token -> indexes into items
	for rows.Next() {
		var it CandidateItem
		var payload, features string
		if err := rows.Scan(&it.ID, &it.Title, &payload, &it.Repo, &features); err != nil {
			rows.Close() //nolint:sqlclosecheck // closed before the next statement
			return nil, 0, err
		}
		it.Payload, it.Trust = json.RawMessage(payload), trustOf(features)
		items = append(items, it)
		for _, t := range LinkTokens(it.Payload) {
			tokens[t] = append(tokens[t], len(items)-1)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, 0, err
	}
	rows.Close()

	known := map[[2]string]bool{}
	prows, err := s.db.QueryContext(ctx, `SELECT work_item_a, work_item_b FROM link_proposal`)
	if err != nil {
		return nil, 0, err
	}
	for prows.Next() {
		var a, b string
		if err := prows.Scan(&a, &b); err != nil {
			prows.Close() //nolint:sqlclosecheck // closed before the next statement
			return nil, 0, err
		}
		known[pairKey(a, b)] = true
	}
	if err := prows.Err(); err != nil {
		prows.Close()
		return nil, 0, err
	}
	prows.Close()

	type pair struct{ i, j int }
	shared := map[pair][]string{}
	for tok, idx := range tokens {
		for x := range idx {
			for y := x + 1; y < len(idx); y++ {
				p := pair{idx[x], idx[y]} // idx is ascending: i < j, i is the older
				shared[p] = append(shared[p], tok)
			}
		}
	}
	var out []LinkCandidate
	refused := 0
	for p, toks := range shared {
		a, b := items[p.i], items[p.j]
		if a.Trust != b.Trust {
			refused++
			continue
		}
		if known[pairKey(a.ID, b.ID)] {
			continue
		}
		slices.Sort(toks)
		out = append(out, LinkCandidate{A: a, B: b, Shared: toks})
	}
	sort.Slice(out, func(i, j int) bool {
		if len(out[i].Shared) != len(out[j].Shared) {
			return len(out[i].Shared) > len(out[j].Shared)
		}
		if out[i].A.ID != out[j].A.ID {
			return out[i].A.ID < out[j].A.ID
		}
		return out[i].B.ID < out[j].B.ID
	})
	if len(out) > max {
		out = out[:max]
	}
	return out, refused, nil
}

func pairKey(a, b string) [2]string {
	if b < a {
		a, b = b, a
	}
	return [2]string{a, b}
}
