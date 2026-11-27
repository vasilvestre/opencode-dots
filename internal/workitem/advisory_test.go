package workitem

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"

	"opencode-dots/internal/sources/advisory"
	"opencode-dots/internal/store"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "test", "advisories", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

var testFloor = []string{"auth", "secret"}

func byID(rs []AdvisoryResult) map[string]AdvisoryResult {
	m := map[string]AdvisoryResult{}
	for _, r := range rs {
		m[r.ID] = r
	}
	return m
}

func TestIngestAdvisoriesCreatesClassifiesAndIsIdempotent(t *testing.T) {
	st, ctx := setup(t)
	lock := fixture(t, "composer.lock")

	rs, left, err := IngestAdvisories(ctx, st, "default", "acme/app", fixture(t, "audit.json"), lock, testFloor)
	if err != nil || left != nil {
		t.Fatalf("ingest: %v, left out %v", err, left)
	}
	if len(rs) != 4 {
		t.Fatalf("%d results, want 4", len(rs))
	}
	want := map[string]int{
		"GHSA-cfgh-2345-wxvr": 1, // fixed in 1.4.2, installed 1.3.0
		"GHSA-mmpp-2222-ccgg": 2, // only 3.0.5 fixes it, installed 2.5.1
		"CVE-2023-55555":      3, // no fixed version
		"GHSA-qqrr-3333-vwxx": 3, // critical on acme/auth-bundle, a plain upgrade otherwise
	}
	for id, level := range want {
		r, ok := byID(rs)[id]
		if !ok || r.Level != level || r.Outcome != store.OutcomeNew {
			t.Errorf("%s: %+v, want level %d new", id, r, level)
		}
	}
	if r := byID(rs)["GHSA-qqrr-3333-vwxx"]; len(r.Floor) != 1 || r.Floor[0] != "auth" {
		t.Errorf("floor = %v", r.Floor)
	}
	if r := byID(rs)["GHSA-cfgh-2345-wxvr"]; r.Packages[0].Installed != "1.3.0" || r.Link == "" {
		t.Errorf("package = %+v", r.Packages)
	}

	again, _, err := IngestAdvisories(ctx, st, "default", "acme/app", fixture(t, "audit.json"), lock, testFloor)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range again {
		if r.Outcome != store.OutcomeSeen {
			t.Errorf("%s: %s on a repeat", r.ID, r.Outcome)
		}
	}
	p, _ := st.Projects(ctx, "default", "acme/app")
	items, _ := st.WorkItemsByProject(ctx, p[0].ID)
	if len(items) != 4 {
		t.Fatalf("%d work items after a repeat, want 4", len(items))
	}
	for _, w := range items {
		if len(w.Refs) != 1 || !strings.HasPrefix(w.Refs[0].Ref, "advisory:") || w.Refs[0].SourceEvent == "" {
			t.Errorf("refs of %s = %+v", w.ID, w.Refs)
		}
	}
}

func TestIngestAdvisoriesRecordsAChangedSeverity(t *testing.T) {
	st, ctx := setup(t)
	if _, _, err := IngestAdvisories(ctx, st, "default", "acme/app", fixture(t, "audit.json"), nil, testFloor); err != nil {
		t.Fatal(err)
	}
	rs, _, err := IngestAdvisories(ctx, st, "default", "acme/app", fixture(t, "audit-changed.json"), nil, testFloor)
	if err != nil {
		t.Fatal(err)
	}
	changed := 0
	for _, r := range rs {
		if r.Outcome == store.OutcomeChanged {
			changed++
			if r.ID != "GHSA-cfgh-2345-wxvr" || r.Severity != "critical" {
				t.Errorf("changed %+v", r)
			}
			w, _ := st.WorkItemByRef(ctx, r.Ref)
			ev, _ := st.WorkItemEvents(ctx, w.ID)
			if len(ev) != 2 || ev[1].Kind != "advisory.changed" {
				t.Errorf("events = %+v", ev)
			}
		}
	}
	if changed != 1 {
		t.Fatalf("%d changed, want 1", changed)
	}
}

func TestIngestAdvisoriesHostileTextStaysData(t *testing.T) {
	st, ctx := setup(t)
	rs, _, err := IngestAdvisories(ctx, st, "default", "acme/app", fixture(t, "audit-hostile.json"), nil, testFloor)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rs {
		for _, s := range []string{r.Title, r.Link, r.Reason} {
			if strings.ContainsFunc(s, unicode.IsControl) {
				t.Errorf("%s: control character in %q", r.ID, s)
			}
		}
		if len([]rune(r.Title)) > 200 {
			t.Errorf("%s: title is %d runes", r.ID, len([]rune(r.Title)))
		}
	}
	if r := byID(rs)["GHSA-cfgh-2345-wxvr"]; r.Link != "" {
		t.Errorf("a javascript: link survived: %q", r.Link)
	}
	if r := byID(rs)["GHSA-mmpp-2222-ccgg"]; r.Link != "https://example.test/ok?a=1" {
		t.Errorf("link = %q", r.Link)
	}
	// The injected instructions changed no reference and no level.
	p, _ := st.Projects(ctx, "default", "acme/app")
	items, _ := st.WorkItemsByProject(ctx, p[0].ID)
	if len(items) != 4 {
		t.Fatalf("%d work items, want 4", len(items))
	}
	if r := byID(rs)["GHSA-mmpp-2222-ccgg"]; r.Level != 2 {
		t.Errorf("level = %d (no lock: installed version unknown)", r.Level)
	}
}

func TestIngestAdvisoriesErrors(t *testing.T) {
	st, ctx := setup(t)
	if _, _, err := IngestAdvisories(ctx, st, "default", "acme/missing", fixture(t, "audit.json"), nil, nil); !errors.Is(err, ErrUnknownProject) {
		t.Errorf("unknown project: %v", err)
	}
	if _, _, err := IngestAdvisories(ctx, st, "default", "acme/app", []byte("not json"), nil, nil); err == nil {
		t.Error("accepted text that is not composer audit json")
	}
	if _, _, err := IngestAdvisories(ctx, st, "default", "acme/app", fixture(t, "audit.json"), []byte("{"), nil); err == nil {
		t.Error("accepted a broken composer.lock")
	}
	rs, left, err := IngestAdvisories(ctx, st, "default", "acme/app", fixture(t, "audit-empty.json"), nil, nil)
	if err != nil || left != nil || len(rs) != 0 {
		t.Errorf("empty report: %v %v %v", rs, left, err)
	}
	// An advisory with no GHSA or CVE id is left out, not turned into a reference.
	rs, left, err = IngestAdvisories(ctx, st, "default", "acme/app", []byte(`{"advisories":{"acme/x":[{"packageName":"acme/x","affectedVersions":"<1","title":"advisory:GHSA-cfgh-2345-wxvr","sources":[]}]}}`), nil, nil)
	if err != nil || left == nil || len(rs) != 0 {
		t.Errorf("no id: %v %v %v", rs, left, err)
	}
}

func TestClassifyRules(t *testing.T) {
	lock := map[string]string{"acme/a": "1.3.0", "acme/b": "dev-main", "acme/secret-store": "3.0.0"}
	for _, c := range []struct {
		name     string
		packages string
		severity string
		want     int
	}{
		{"upgrade in the same major", `[{"name":"acme/a","affected":"<1.4.2","fixed_in":["1.4.2"]}]`, "high", 1},
		{"a fix in the same major among several", `[{"name":"acme/a","affected":">=1.0,<1.4.2|>=2.0,<2.1.1","fixed_in":["1.4.2","2.1.1"]}]`, "high", 1},
		{"only a new major", `[{"name":"acme/a","affected":"<2.0.1","fixed_in":["2.0.1"]}]`, "high", 2},
		{"unknown installed", `[{"name":"acme/zzz","affected":"<2.0.1","fixed_in":["2.0.1"]}]`, "high", 2},
		{"a branch is not a release", `[{"name":"acme/b","affected":"<2.0.1","fixed_in":["2.0.1"]}]`, "high", 2},
		{"no fix", `[{"name":"acme/a","affected":">=1.0","fixed_in":[]}]`, "low", 3},
		{"worst package wins", `[{"name":"acme/a","affected":"<1.4.2","fixed_in":["1.4.2"]},{"name":"acme/b","affected":">=1","fixed_in":[]}]`, "low", 3},
		{"critical but not sensitive", `[{"name":"acme/a","affected":"<1.4.2","fixed_in":["1.4.2"]}]`, "critical", 1},
		{"critical and sensitive", `[{"name":"acme/secret-store","affected":"<3.0.1","fixed_in":["3.0.1"]}]`, "critical", 3},
		{"sensitive but only high", `[{"name":"acme/secret-store","affected":"<3.0.1","fixed_in":["3.0.1"]}]`, "high", 1},
	} {
		p := payloadOf(t, c.severity, c.packages)
		got, reason, _ := ClassifyAdvisory(p, lock, testFloor)
		if got != c.want || reason == "" {
			t.Errorf("%s: level %d (%s), want %d", c.name, got, reason, c.want)
		}
	}
}

func payloadOf(t *testing.T, severity, packages string) advisory.Payload {
	t.Helper()
	p := advisory.Payload{ID: "CVE-2024-0001", Severity: severity}
	if err := json.Unmarshal([]byte(packages), &p.Packages); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestIngestAdvisoriesRefusesAReferenceHeldByAnotherProject(t *testing.T) {
	st, ctx := setup(t)
	if _, _, err := st.UpsertProject(ctx, "default", "acme/lib", 1, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := IngestAdvisories(ctx, st, "default", "acme/app", fixture(t, "audit.json"), nil, testFloor); err != nil {
		t.Fatal(err)
	}
	rs, _, err := IngestAdvisories(ctx, st, "default", "acme/lib", fixture(t, "audit.json"), nil, testFloor)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 4 {
		t.Fatalf("%d results, want 4", len(rs))
	}
	for _, r := range rs {
		if r.Outcome != store.OutcomeRefused || r.WorkItem != "" {
			t.Errorf("%s: %+v", r.ID, r)
		}
	}
	p, _ := st.Projects(ctx, "default", "acme/lib")
	if items, _ := st.WorkItemsByProject(ctx, p[0].ID); len(items) != 0 {
		t.Fatalf("%d work items for the second repository, want 0", len(items))
	}
}
