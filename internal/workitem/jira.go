package workitem

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"opencode-dots/internal/sources/jira"
	"opencode-dots/internal/store"
)

// JiraResult is what recording one Jira issue did. Every text field is clean: no control
// characters and capped.
type JiraResult struct {
	Outcome  store.Outcome `json:"status"`
	WorkItem string        `json:"work_item"`
	Ref      string        `json:"ref"`
	Key      string        `json:"key"`
	Project  string        `json:"project"`
	Repo     string        `json:"repo"`
	Summary  string        `json:"summary"`
	Status   string        `json:"issue_status,omitempty"`
	Priority string        `json:"priority,omitempty"`
}

// IngestJira records the issues of one search-issues response as work items of the
// repository that the owner mapped to the Jira project (repos.yaml, `jira: ABC`). Only the
// issues themselves are recorded, referenced `jira:ABC-12` from the structured key; comments
// are left out for now, since they would replace the issue's payload on each poll. An issue
// whose project is not the mapped one is left out and named in leftOut: a moved issue never
// lands on a repository it was not mapped to. A repeat of an unchanged issue adds nothing, an
// edit (a new `updated` stamp) replaces the payload and records an event.
func IngestJira(ctx context.Context, st *store.Store, workspace, repo, project string, raw []byte) (results []JiraResult, leftOut error, err error) {
	projects, err := st.Projects(ctx, workspace, repo)
	if err != nil {
		return nil, nil, err
	}
	if len(projects) == 0 {
		return nil, nil, fmt.Errorf("%w: %s", ErrUnknownProject, repo)
	}
	table := jira.Projects{project: repo}
	if err := table.Validate(); err != nil {
		return nil, nil, err
	}
	envs, leftOut := jira.Adapter{Projects: table}.Parse(raw)
	if envs == nil {
		return nil, nil, leftOut
	}
	who, err := st.EnsureActor(ctx, store.Actor{Kind: "bot", DisplayName: jira.Actor, Identities: map[string]string{"jira": jira.Actor}})
	if err != nil {
		return nil, nil, err
	}
	var skipped []error
	for _, env := range envs {
		if env.Kind != jira.KindSeen {
			continue
		}
		if err := env.Validate(); err != nil {
			return results, leftOut, err
		}
		var p jira.Payload
		if err := json.Unmarshal(env.Payload, &p); err != nil {
			return results, leftOut, fmt.Errorf("workitem: payload of %s: %w", env.Ref, err)
		}
		if p.Project != project || p.DiagnosisOnly {
			skipped = append(skipped, fmt.Errorf("%s is not in project %s, left out", env.Ref, project))
			continue
		}
		w, outcome, err := st.RecordSourced(ctx, store.Sourced{
			ProjectID:   projects[0].ID,
			Ref:         env.Ref,
			Title:       cleanText(p.Key+": "+p.Summary, maxTitle),
			Payload:     env.Payload,
			SourceEvent: env.Key,
			Kind:        "jira",
			ActorID:     who.ID,
		})
		if errors.Is(err, store.ErrWrongProject) {
			skipped = append(skipped, fmt.Errorf("%s belongs to a work item of another project, left as it is", env.Ref))
			continue
		}
		if err != nil {
			return results, leftOut, fmt.Errorf("%s: %w", env.Ref, err)
		}
		results = append(results, JiraResult{
			Outcome: outcome, WorkItem: w.ID, Ref: env.Ref, Key: p.Key, Project: project, Repo: repo,
			Summary: cleanText(p.Summary, maxTitle), Status: cleanText(p.Status, maxName), Priority: cleanText(p.Priority, maxName),
		})
	}
	return results, errors.Join(append(skipped, leftOut)...), nil
}
