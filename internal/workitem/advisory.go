package workitem

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"opencode-dots/internal/sources"
	"opencode-dots/internal/sources/advisory"
	"opencode-dots/internal/store"
)

// Text from an advisory is data from a third party. Before it leaves this package (a
// title, a link, a package name) it is stripped of control characters and cut.
const (
	maxTitle = 200
	maxName  = 100
	maxLink  = 300
)

// AdvisoryPackage is one affected package of a result, with the version the lock file
// installs ("" when it is not known).
type AdvisoryPackage struct {
	Name      string   `json:"name"`
	Installed string   `json:"installed,omitempty"`
	Affected  string   `json:"affected"`
	FixedIn   []string `json:"fixed_in"`
}

// AdvisoryResult is what recording one advisory did, plus its classification (level 1, 2
// or 3 as in docs/classification.md). Every text field is clean: no control characters,
// capped, and Link is empty unless it is an http or https URL.
type AdvisoryResult struct {
	Outcome    store.Outcome     `json:"status"`
	WorkItem   string            `json:"work_item"`
	Ref        string            `json:"ref"`
	ID         string            `json:"id"`
	Severity   string            `json:"severity"`
	Level      int               `json:"level"`
	Reason     string            `json:"reason"`
	Floor      []string          `json:"floor"`
	Title      string            `json:"title"`
	Link       string            `json:"link,omitempty"`
	CVEs       []string          `json:"cves"`
	Packages   []AdvisoryPackage `json:"packages"`
	ProjectRef string            `json:"repo"`
}

// IngestAdvisories records every advisory of one `composer audit --format=json` report as
// a work item of the repository, through the advisory adapter. The reference is
// `advisory:<id>@<owner>/<repo>` with the id taken from the structured GHSA or CVE field,
// never from text, so the same advisory in two repositories is two work items. An item
// still stored under the old `advisory:<id>` form for this repository is the same item: its
// reference is rewritten and an event recorded. A repeat adds nothing; a changed payload replaces the stored one and records an event.
//
// lock is the repository's composer.lock (may be nil): it gives the installed versions the
// classification needs. floor is the sensitive vocabulary of workflows/data/classify-floor.txt.
//
// leftOut reports advisories the adapter could not identify; the others are still recorded.
func IngestAdvisories(ctx context.Context, st *store.Store, workspace, repo string, raw, lock []byte, floor []string) (results []AdvisoryResult, leftOut error, err error) {
	projects, err := st.Projects(ctx, workspace, repo)
	if err != nil {
		return nil, nil, err
	}
	if len(projects) == 0 {
		return nil, nil, fmt.Errorf("%w: %s", ErrUnknownProject, repo)
	}
	installed, err := ParseLock(lock)
	if err != nil {
		return nil, nil, err
	}
	envs, leftOut := advisory.Adapter{Repo: repo}.Parse(raw)
	if envs == nil {
		// The adapter returns a non-nil slice whenever the report itself was readable.
		return nil, nil, leftOut
	}
	who, err := st.EnsureActor(ctx, store.Actor{Kind: "bot", DisplayName: advisory.Actor, Identities: map[string]string{"advisory": advisory.Actor}})
	if err != nil {
		return nil, nil, err
	}
	for _, env := range envs {
		if err := env.Validate(); err != nil {
			return results, leftOut, err
		}
		var p advisory.Payload
		if err := json.Unmarshal(env.Payload, &p); err != nil {
			return results, leftOut, fmt.Errorf("workitem: payload of %s: %w", env.Ref, err)
		}
		w, outcome, err := st.RecordSourced(ctx, store.Sourced{
			ProjectID:   projects[0].ID,
			Ref:         env.Ref,
			LegacyRef:   "advisory:" + p.ID,
			Title:       cleanText("Advisory "+p.ID+": "+p.Title, maxTitle),
			Payload:     env.Payload,
			SourceEvent: env.Key,
			Kind:        "advisory",
			ActorID:     who.ID,
		})
		refused := errors.Is(err, store.ErrWrongProject)
		if err != nil && !refused {
			return results, leftOut, fmt.Errorf("%s: %w", env.Ref, err)
		}
		if refused {
			// The reference is held by a work item of another project (ADR 0023): it is not
			// moved, and the other advisories of the report are still recorded. With scoped
			// references this only happens when two projects share one repository name.
			outcome = store.OutcomeRefused
		}
		level, reason, hits := ClassifyAdvisory(p, installed, floor)
		res := AdvisoryResult{
			Outcome: outcome, WorkItem: w.ID, Ref: env.Ref, ID: p.ID, Severity: p.Severity,
			Level: level, Reason: reason, Floor: hits, Title: cleanText(p.Title, maxTitle),
			Link: cleanLink(p.Link), CVEs: []string{}, ProjectRef: repo,
		}
		for _, c := range p.CVEs {
			if _, err := sources.NewRef(sources.Advisory, c); err == nil {
				res.CVEs = append(res.CVEs, c)
			}
		}
		for _, pk := range p.Packages {
			ap := AdvisoryPackage{Name: cleanText(pk.Name, maxName), Installed: installed[strings.ToLower(pk.Name)], Affected: cleanText(pk.Affected, maxLink), FixedIn: []string{}}
			ap.Installed = cleanText(ap.Installed, maxName)
			for _, f := range pk.FixedIn {
				ap.FixedIn = append(ap.FixedIn, cleanText(f, maxName))
			}
			res.Packages = append(res.Packages, ap)
		}
		results = append(results, res)
	}
	return results, leftOut, nil
}

// ParseLock reads the installed versions out of a composer.lock: package name (lower case)
// to version, dev packages included. An empty lock gives an empty map.
func ParseLock(lock []byte) (map[string]string, error) {
	out := map[string]string{}
	if len(strings.TrimSpace(string(lock))) == 0 {
		return out, nil
	}
	var doc struct {
		Packages    []struct{ Name, Version string } `json:"packages"`
		PackagesDev []struct{ Name, Version string } `json:"packages-dev"`
	}
	if err := json.Unmarshal(lock, &doc); err != nil {
		return nil, fmt.Errorf("workitem: not a composer.lock: %w", err)
	}
	for _, list := range [][]struct{ Name, Version string }{doc.Packages, doc.PackagesDev} {
		for _, p := range list {
			if p.Name != "" && p.Version != "" {
				out[strings.ToLower(p.Name)] = p.Version
			}
		}
	}
	return out, nil
}

// ClassifyAdvisory rates an advisory with fixed rules, no agent (ADR 0040):
//
//   - level 1: every affected package has a fixed version in the same major version as the
//     installed one, so a plain upgrade fixes it;
//   - level 2: a fix exists but only in a new major version, or the installed version is
//     not known so the major cannot be compared;
//   - level 3: some package has no fixed version, or the advisory is critical on a package
//     whose name starts a word of the sensitive vocabulary (the floor, as in
//     workflows/_classify-item.yaml).
//
// The advisory text (title, link) is never read: only the structured fields are.
func ClassifyAdvisory(p advisory.Payload, installed map[string]string, floor []string) (level int, reason string, hits []string) {
	level, reason = 1, "a fixed version exists in the installed major version of every affected package"
	raise := func(l int, why string) {
		if l > level {
			level, reason = l, why
		}
	}
	for _, pk := range p.Packages {
		name := cleanText(pk.Name, maxName)
		have := installed[strings.ToLower(pk.Name)]
		switch {
		case len(pk.FixedIn) == 0:
			raise(3, "no fixed version for "+name)
		case have == "":
			raise(2, "installed version of "+name+" is not known")
		default:
			cur, ok := parseVersion(have)
			if !ok {
				raise(2, "installed version of "+name+" is not a release version")
				break
			}
			same := false
			for _, f := range pk.FixedIn {
				if v, ok := parseVersion(f); ok && v[0] == cur[0] && compare(v, cur) > 0 {
					same = true
				}
			}
			if !same {
				raise(2, "only a new major version fixes "+name)
			}
		}
		if p.Severity == "critical" {
			for _, w := range matchFloor(pk.Name, floor) {
				hits = appendUnique(hits, w)
			}
		}
	}
	if len(hits) > 0 {
		level = 3
		reason = "critical on a sensitive package (" + strings.Join(hits, ", ") + ")"
	}
	return level, reason, hits
}

var words = regexp.MustCompile(`[A-Za-z0-9]+`)

// matchFloor returns the floor words that start a word of the package name, as the
// workflow's floor does for prose: "auth" matches "auth-bundle" and "authentication".
func matchFloor(name string, floor []string) []string {
	var hits []string
	for _, w := range words.FindAllString(strings.ToLower(name), -1) {
		for _, f := range floor {
			if f != "" && strings.HasPrefix(w, strings.ToLower(f)) {
				hits = appendUnique(hits, strings.ToLower(f))
			}
		}
	}
	return hits
}

// ParseFloor reads the words of classify-floor.txt: one per line, blank lines and lines
// starting with # skipped.
func ParseFloor(data []byte) []string {
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, strings.ToLower(line))
	}
	return out
}

func appendUnique(list []string, s string) []string {
	for _, v := range list {
		if v == s {
			return list
		}
	}
	return append(list, s)
}

var versionHead = regexp.MustCompile(`^v?(\d+)(?:\.(\d+))?(?:\.(\d+))?`)

// parseVersion reads major.minor.patch from a release such as "1.4.2" or "v2.0". A branch
// ("dev-main") or anything else is not a release version.
func parseVersion(s string) ([3]int, bool) {
	var v [3]int
	m := versionHead.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return v, false
	}
	for i := 0; i < 3; i++ {
		if m[i+1] == "" {
			continue
		}
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return v, false
		}
		v[i] = n
	}
	return v, true
}

func compare(a, b [3]int) int {
	for i := range a {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

// cleanText drops control and format characters (including line breaks and bidi marks) and
// cuts to max runes. The text stays data: this only makes it safe to print and to store.
func cleanText(s string, max int) string {
	var b strings.Builder
	n := 0
	for _, r := range s {
		switch {
		case unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == ' ' || r == ' ':
			r = ' '
		}
		if r == ' ' && (b.Len() == 0 || strings.HasSuffix(b.String(), " ")) {
			continue
		}
		if n >= max {
			break
		}
		b.WriteRune(r)
		n++
	}
	return strings.TrimSpace(b.String())
}

// cleanLink keeps an absolute http or https URL without spaces or markup characters, and
// nothing else.
func cleanLink(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > maxLink || strings.ContainsFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r) || strings.ContainsRune("<>\"'`()[]\\", r)
	}) {
		return ""
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	return s
}
