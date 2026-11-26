// Package workitem turns tracker events into work items (ADR 0023). The first adapter is
// GitHub's. An adapter reads structured fields only (the number, the head branch, the links
// the tracker itself records between an issue and a pull request): text in an issue or a pull
// request is data, so no reference is ever parsed from a title or a body.
package workitem

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"opencode-dots/internal/store"
)

// ErrUnknownProject is returned for an event about a repository the workspace does not list.
var ErrUnknownProject = errors.New("workitem: repository is not a project of this workspace")

// ErrConflict is returned when the references of one event already belong to different work
// items. Merging them is an explicit action, never a side effect of an event (ADR 0023), so
// nothing is attached.
var ErrConflict = errors.New("workitem: references belong to different work items")

// GitHubEvent is the common envelope of an event from GitHub, after the tracker's own fields
// have been read. There is no body field on purpose.
type GitHubEvent struct {
	Repo   string // owner/name
	Kind   string // the awf-gh event: pr-opened, pr-updated, checks-failed, review-comment, issue-opened, issue-labeled, command
	Number int    // the issue or pull request number
	// Branch is the head branch of a pull request; empty for an issue.
	Branch string
	// LinkedIssues are the issues GitHub itself records as closed by this pull request.
	LinkedIssues []int
	Actor        string // the login that caused the event
	Title        string
}

// Result says what an observed event did.
type Result struct {
	WorkItem store.WorkItem
	Created  bool
	Attached []string // references added to an existing work item
}

// Observe finds or creates the work item an event is about. The references are the issue or
// pull request, its head branch, and the issues GitHub links to it. With none of them known a
// work item is created; with one work item among them the missing references are attached to
// it; with two or more it is a conflict and nothing changes.
func Observe(ctx context.Context, st *store.Store, workspace string, ev GitHubEvent) (Result, error) {
	var res Result
	if ev.Repo == "" || ev.Number <= 0 || ev.Actor == "" {
		return res, errors.New("workitem: an event needs a repository, a number and an actor")
	}
	projects, err := st.Projects(ctx, workspace, ev.Repo)
	if err != nil {
		return res, err
	}
	if len(projects) == 0 {
		return res, fmt.Errorf("%w: %s", ErrUnknownProject, ev.Repo)
	}
	project := projects[0]
	who, err := actorFor(ctx, st, ev.Actor)
	if err != nil {
		return res, err
	}

	refs := []string{fmt.Sprintf("github:%s#%d", ev.Repo, ev.Number)}
	if ev.Branch != "" {
		refs = append(refs, fmt.Sprintf("branch:%s:%s", ev.Repo, ev.Branch))
	}
	linked := append([]int(nil), ev.LinkedIssues...)
	sort.Ints(linked)
	for _, n := range linked {
		if n > 0 && n != ev.Number {
			refs = append(refs, fmt.Sprintf("github:%s#%d", ev.Repo, n))
		}
	}

	owners := map[string]store.WorkItem{}
	var free []string
	for _, r := range refs {
		w, err := st.WorkItemByRef(ctx, r)
		switch {
		case err == nil:
			owners[w.ID] = w
		case errors.Is(err, store.ErrNotFound):
			free = append(free, r)
		default:
			return res, err
		}
	}
	switch len(owners) {
	case 0:
		w, err := st.CreateWorkItem(ctx, project.ID, ev.Title, who.ID, refs...)
		if err != nil {
			return res, err
		}
		res.WorkItem, res.Created = w, true
	case 1:
		var w store.WorkItem
		for _, w = range owners {
		}
		for _, r := range free {
			if w, err = st.AttachRef(ctx, w.ID, r, ev.Kind, who.ID); err != nil {
				return res, err
			}
			res.Attached = append(res.Attached, r)
		}
		res.WorkItem = w
	default:
		ids := make([]string, 0, len(owners))
		for id := range owners {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		return res, fmt.Errorf("%w: %s", ErrConflict, strings.Join(ids, ", "))
	}
	return res, nil
}

// actorFor returns the actor that carries a GitHub login, creating it the first time. A login
// ending in [bot] is a bot.
func actorFor(ctx context.Context, st *store.Store, login string) (store.Actor, error) {
	kind := "human"
	if strings.HasSuffix(login, "[bot]") {
		kind = "bot"
	}
	return st.EnsureActor(ctx, store.Actor{Kind: kind, DisplayName: login, Identities: map[string]string{"github": login}})
}
