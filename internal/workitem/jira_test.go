package workitem

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"opencode-dots/internal/store"
)

func jiraFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "sources", "jira", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestIngestJiraRecordsMappedIssuesOnly(t *testing.T) {
	st, ctx := setup(t)
	raw := jiraFixture(t, "search.json")

	rs, left, err := IngestJira(ctx, st, "default", "acme/app", "ABC", raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 || rs[0].Ref != "jira:ABC-12" || rs[0].Outcome != store.OutcomeNew || rs[0].Repo != "acme/app" {
		t.Fatalf("results = %+v", rs)
	}
	// ZZZ-3 is in another project: left out, never recorded; comments are not recorded.
	if left == nil || !strings.Contains(left.Error(), "ZZZ-3") {
		t.Fatalf("left out = %v", left)
	}
	if _, err := st.WorkItemByRef(ctx, "jira:ZZZ-3"); err == nil {
		t.Fatal("an issue of another project was recorded")
	}

	rs, _, err = IngestJira(ctx, st, "default", "acme/app", "ABC", raw)
	if err != nil || len(rs) != 1 || rs[0].Outcome != store.OutcomeSeen {
		t.Fatalf("repeat = %+v, %v", rs, err)
	}

	edited := strings.Replace(string(raw), "11:15:30.456", "13:00:00.000", 1)
	rs, _, err = IngestJira(ctx, st, "default", "acme/app", "ABC", []byte(edited))
	if err != nil || len(rs) != 1 || rs[0].Outcome != store.OutcomeChanged {
		t.Fatalf("edit = %+v, %v", rs, err)
	}
	w, err := st.WorkItemByRef(ctx, "jira:ABC-12")
	if err != nil {
		t.Fatal(err)
	}
	evs, err := st.WorkItemEvents(ctx, w.ID)
	if err != nil || len(evs) != 2 || evs[0].Kind != "jira.created" || evs[1].Kind != "jira.changed" {
		t.Fatalf("events = %+v, %v", evs, err)
	}
}

func TestIngestJiraHostileTextNeverMovesTheRepository(t *testing.T) {
	st, ctx := setup(t)
	rs, _, err := IngestJira(ctx, st, "default", "acme/app", "ABC", jiraFixture(t, "hostile.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rs {
		if r.Repo != "acme/app" || !strings.HasPrefix(r.Ref, "jira:ABC-") {
			t.Fatalf("hostile text reached the result: %+v", r)
		}
	}
}

func TestIngestJiraUnknownProject(t *testing.T) {
	st, ctx := setup(t)
	if _, _, err := IngestJira(ctx, st, "default", "acme/other", "ABC", jiraFixture(t, "search.json")); err == nil {
		t.Fatal("a repository outside the database was accepted")
	}
}
