package workitem

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"opencode-dots/internal/store"
)

func setup(t *testing.T) (*store.Store, context.Context) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "dots.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	if _, _, err := st.UpsertProject(ctx, "default", "acme/app", 0, nil); err != nil {
		t.Fatal(err)
	}
	return st, ctx
}

func refsOf(w store.WorkItem) []string {
	var out []string
	for _, r := range w.Refs {
		out = append(out, r.Ref)
	}
	return out
}

func TestAPullRequestWithNoIssueMakesAWorkItem(t *testing.T) {
	st, ctx := setup(t)
	res, err := Observe(ctx, st, "default", GitHubEvent{Repo: "acme/app", Kind: "pr-opened", Number: 7, Branch: "fix-login", Actor: "ada", Title: "Fix the login"})
	if err != nil || !res.Created {
		t.Fatalf("result = %+v, %v", res, err)
	}
	got := refsOf(res.WorkItem)
	if len(got) != 2 || got[0] == got[1] {
		t.Errorf("references = %v, want the pull request and its branch", got)
	}
	if res.WorkItem.Title != "Fix the login" || res.WorkItem.State != "open" {
		t.Errorf("work item = %+v", res.WorkItem)
	}
}

func TestTheSameEventAgainChangesNothing(t *testing.T) {
	st, ctx := setup(t)
	ev := GitHubEvent{Repo: "acme/app", Kind: "pr-opened", Number: 7, Branch: "fix-login", Actor: "ada", Title: "t"}
	first, _ := Observe(ctx, st, "default", ev)
	ev.Kind = "pr-updated"
	again, err := Observe(ctx, st, "default", ev)
	if err != nil || again.Created || len(again.Attached) != 0 || again.WorkItem.ID != first.WorkItem.ID {
		t.Fatalf("second observation = %+v, %v", again, err)
	}
	p, _ := st.ProjectByRepo(ctx, "default", "acme/app")
	if items, _ := st.WorkItemsByProject(ctx, p.ID); len(items) != 1 {
		t.Errorf("%d work items, want 1", len(items))
	}
}

func TestAPullRequestJoinsTheWorkItemOfItsIssue(t *testing.T) {
	st, ctx := setup(t)
	issue, err := Observe(ctx, st, "default", GitHubEvent{Repo: "acme/app", Kind: "issue-opened", Number: 12, Actor: "ada", Title: "Login fails"})
	if err != nil {
		t.Fatal(err)
	}
	pr, err := Observe(ctx, st, "default", GitHubEvent{Repo: "acme/app", Kind: "pr-opened", Number: 14, Branch: "fix-login", LinkedIssues: []int{12}, Actor: "grace", Title: "Fix"})
	if err != nil || pr.Created || pr.WorkItem.ID != issue.WorkItem.ID {
		t.Fatalf("the pull request should join the issue's work item: %+v, %v", pr, err)
	}
	if len(pr.Attached) != 2 || len(pr.WorkItem.Refs) != 3 {
		t.Errorf("attached %v, references %v", pr.Attached, refsOf(pr.WorkItem))
	}
}

func TestTwoWorkItemsAreNeverMergedByAnEvent(t *testing.T) {
	st, ctx := setup(t)
	a, _ := Observe(ctx, st, "default", GitHubEvent{Repo: "acme/app", Kind: "issue-opened", Number: 1, Actor: "ada"})
	b, _ := Observe(ctx, st, "default", GitHubEvent{Repo: "acme/app", Kind: "pr-opened", Number: 2, Branch: "other", Actor: "ada"})
	_, err := Observe(ctx, st, "default", GitHubEvent{Repo: "acme/app", Kind: "pr-updated", Number: 2, Branch: "other", LinkedIssues: []int{1}, Actor: "ada"})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("error = %v, want ErrConflict", err)
	}
	after, _ := st.WorkItemByRef(ctx, b.WorkItem.Refs[0].Ref)
	if len(after.Refs) != len(b.WorkItem.Refs) {
		t.Error("a conflicting event attached references")
	}
	if first, _ := st.WorkItemByRef(ctx, a.WorkItem.Refs[0].Ref); len(first.Refs) != len(a.WorkItem.Refs) {
		t.Error("a conflicting event attached references to the other work item")
	}
}

func TestAnEventForAnUnlistedRepositoryIsRefused(t *testing.T) {
	st, ctx := setup(t)
	_, err := Observe(ctx, st, "default", GitHubEvent{Repo: "acme/other", Kind: "pr-opened", Number: 1, Actor: "ada"})
	if !errors.Is(err, ErrUnknownProject) {
		t.Errorf("error = %v, want ErrUnknownProject", err)
	}
}

func TestAnEventNeedsItsFields(t *testing.T) {
	st, ctx := setup(t)
	for name, ev := range map[string]GitHubEvent{
		"no repo":   {Number: 1, Actor: "ada"},
		"no number": {Repo: "acme/app", Actor: "ada"},
		"no actor":  {Repo: "acme/app", Number: 1},
	} {
		if _, err := Observe(ctx, st, "default", ev); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestActorsAreReusedAndBotsAreBots(t *testing.T) {
	st, ctx := setup(t)
	a, _ := Observe(ctx, st, "default", GitHubEvent{Repo: "acme/app", Kind: "issue-opened", Number: 1, Actor: "ada"})
	b, _ := Observe(ctx, st, "default", GitHubEvent{Repo: "acme/app", Kind: "issue-opened", Number: 2, Actor: "ada"})
	if a.WorkItem.CreatedBy != b.WorkItem.CreatedBy {
		t.Errorf("the same login got two actors: %s and %s", a.WorkItem.CreatedBy, b.WorkItem.CreatedBy)
	}
	bot, _ := Observe(ctx, st, "default", GitHubEvent{Repo: "acme/app", Kind: "pr-opened", Number: 3, Branch: "deps", Actor: "dependabot[bot]"})
	who, err := st.EnsureActor(ctx, store.Actor{Kind: "bot", DisplayName: "dependabot[bot]", Identities: map[string]string{"github": "dependabot[bot]"}})
	if err != nil || who.Kind != "bot" || bot.WorkItem.CreatedBy != who.ID {
		t.Errorf("bot actor = %+v, %v", who, err)
	}
}

func TestALinkToItselfIsIgnored(t *testing.T) {
	st, ctx := setup(t)
	res, err := Observe(ctx, st, "default", GitHubEvent{Repo: "acme/app", Kind: "pr-opened", Number: 5, LinkedIssues: []int{5, 0, -1}, Actor: "ada"})
	if err != nil || len(res.WorkItem.Refs) != 1 {
		t.Errorf("references = %v, %v", refsOf(res.WorkItem), err)
	}
}
