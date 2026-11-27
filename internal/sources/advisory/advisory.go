// Package advisory is the adapter for `composer audit --format=json` (ADR 0039).
//
// One envelope per advisory id, whichever packages it hits, in the repository being audited:
// the reference is advisory:<id>@<owner>/<repo>, so the same advisory in two repositories is
// two work items and a repeat in one repository is the same work item. The id comes from the structured sources or cve fields, never from titles.
package advisory

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"opencode-dots/internal/sources"
)

// Kind is the envelope kind this adapter emits.
const Kind = "advisory.reported"

// Actor identifies the origin of the events.
const Actor = "composer-audit"

// Adapter parses composer audit output. The zero value is ready to use.
type Adapter struct {
	// Repo is the OWNER/REPO the report was produced for. It is required: it scopes every
	// reference.
	Repo string
	// Now stands in for the report date when an advisory carries none.
	Now func() time.Time
}

// Package is one affected package of an advisory.
type Package struct {
	Name     string   `json:"name"`
	Affected string   `json:"affected"`
	FixedIn  []string `json:"fixed_in"`
}

// Payload is the envelope payload. Title and Link are data from a third party.
type Payload struct {
	ID       string    `json:"id"`
	CVEs     []string  `json:"cves"`
	Severity string    `json:"severity"`
	Title    string    `json:"title"`
	Link     string    `json:"link,omitempty"`
	Packages []Package `json:"packages"`
}

type report struct {
	// Composer prints [] instead of {} when there is nothing to report.
	Advisories json.RawMessage `json:"advisories"`
}

type entry struct {
	PackageName      string  `json:"packageName"`
	AffectedVersions string  `json:"affectedVersions"`
	Title            string  `json:"title"`
	CVE              *string `json:"cve"`
	Link             string  `json:"link"`
	ReportedAt       string  `json:"reportedAt"`
	Severity         *string `json:"severity"`
	Sources          []struct {
		Name     string `json:"name"`
		RemoteID string `json:"remoteId"`
	} `json:"sources"`
}

var _ sources.Adapter = Adapter{}

// Tracker implements sources.Adapter.
func (Adapter) Tracker() sources.Tracker { return sources.Advisory }

// Parse implements sources.Adapter. An advisory with neither a GHSA nor a CVE id is left
// out and reported in the error, the others are still returned.
func (a Adapter) Parse(raw []byte) ([]sources.Envelope, error) {
	if _, err := sources.AdvisoryRef("CVE-2000-0000", a.Repo); err != nil {
		return nil, fmt.Errorf("advisory: Repo %q is not OWNER/REPO", a.Repo)
	}
	var rep report
	if err := json.Unmarshal(raw, &rep); err != nil {
		return nil, fmt.Errorf("advisory: not composer audit json: %w", err)
	}
	var byPackage map[string][]entry
	if s := strings.TrimSpace(string(rep.Advisories)); s != "" && s != "null" && s != "[]" {
		if err := json.Unmarshal(rep.Advisories, &byPackage); err != nil {
			return nil, fmt.Errorf("advisory: bad advisories: %w", err)
		}
	}
	groups := map[string]*Payload{}
	times := map[string]time.Time{}
	var errs []error
	pkgs := make([]string, 0, len(byPackage))
	for p := range byPackage {
		pkgs = append(pkgs, p)
	}
	slices.Sort(pkgs)
	for _, p := range pkgs {
		for _, e := range byPackage[p] {
			id := advisoryID(e)
			if id == "" {
				errs = append(errs, fmt.Errorf("advisory: %s: %q has no GHSA or CVE id", p, e.Title))
				continue
			}
			g := groups[id]
			if g == nil {
				g = &Payload{ID: id, Title: e.Title, Link: e.Link, Severity: "unknown"}
				groups[id] = g
			}
			if e.CVE != nil && *e.CVE != "" && !slices.Contains(g.CVEs, *e.CVE) {
				g.CVEs = append(g.CVEs, *e.CVE)
			}
			if e.Severity != nil && rank(*e.Severity) > rank(g.Severity) {
				g.Severity = strings.ToLower(*e.Severity)
			}
			name := e.PackageName
			if name == "" {
				name = p
			}
			g.Packages = append(g.Packages, Package{Name: name, Affected: e.AffectedVersions, FixedIn: fixedIn(e.AffectedVersions)})
			if t, err := time.Parse(time.RFC3339, e.ReportedAt); err == nil && (times[id].IsZero() || t.Before(times[id])) {
				times[id] = t
			}
		}
	}
	ids := make([]string, 0, len(groups))
	for id := range groups {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	out := make([]sources.Envelope, 0, len(ids))
	for _, id := range ids {
		g := groups[id]
		slices.Sort(g.CVEs)
		sort.Slice(g.Packages, func(i, j int) bool { return g.Packages[i].Name < g.Packages[j].Name })
		body, err := json.Marshal(g)
		if err != nil {
			return out, err
		}
		t := times[id]
		if t.IsZero() {
			t = a.now()
		}
		rr, err := sources.AdvisoryRef(id, a.Repo)
		if err != nil {
			return out, err
		}
		ref := rr.String()
		out = append(out, sources.Envelope{
			Ref: ref, Kind: Kind, Actor: Actor, Time: t, Payload: body,
			Key: sources.IdempotencyKey(ref, Kind),
		})
	}
	return out, errors.Join(errs...)
}

func (a Adapter) now() time.Time {
	if a.Now != nil {
		return a.Now().UTC()
	}
	return time.Now().UTC()
}

// advisoryID takes the id from structured fields only: a GitHub source first, then the CVE.
func advisoryID(e entry) string {
	for _, s := range e.Sources {
		if sources.AdvisoryID.MatchString(s.RemoteID) && strings.HasPrefix(s.RemoteID, "GHSA-") {
			return s.RemoteID
		}
	}
	if e.CVE != nil {
		if sources.AdvisoryID.MatchString(*e.CVE) {
			return *e.CVE
		}
	}
	return ""
}

var severities = map[string]int{"low": 1, "medium": 2, "high": 3, "critical": 4}

func rank(s string) int { return severities[strings.ToLower(s)] }

// fixed versions are the upper bounds of the affected ranges: "<1.4.2" is fixed in 1.4.2.
var upperBound = regexp.MustCompile(`(?:^|,)<([0-9][^,|\s]*)`)

func fixedIn(affected string) []string {
	var out []string
	for _, rng := range strings.Split(affected, "|") {
		if m := upperBound.FindStringSubmatch(strings.TrimSpace(rng)); m != nil && !slices.Contains(out, m[1]) {
			out = append(out, m[1])
		}
	}
	return out
}
