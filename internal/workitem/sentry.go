package workitem

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"opencode-dots/internal/sources/sentry"
	"opencode-dots/internal/store"
)

// SentryResult is what recording one Sentry issue did. Every text field is clean.
type SentryResult struct {
	Outcome  store.Outcome `json:"status"`
	WorkItem string        `json:"work_item"`
	Ref      string        `json:"ref"`
	ID       string        `json:"id"`
	ShortID  string        `json:"short_id,omitempty"`
	Level    string        `json:"level,omitempty"`
	Title    string        `json:"title"`
	Repo     string        `json:"repo"`
}

// IngestSentry records the issues of one list-issues response as work items of the repository
// that the owner mapped to the Sentry project (repos.yaml, `sentry: org/project`). The reference
// is `sentry:org/project#id` from structured fields. An issue of another project is left out.
// The event count and the last-seen stamp are dropped before storing: they change on every poll
// and would record a change each time; a new status, level or title still does.
func IngestSentry(ctx context.Context, st *store.Store, workspace, repo, org, project string, raw []byte) (results []SentryResult, leftOut error, err error) {
	projects, err := st.Projects(ctx, workspace, repo)
	if err != nil {
		return nil, nil, err
	}
	if len(projects) == 0 {
		return nil, nil, fmt.Errorf("%w: %s", ErrUnknownProject, repo)
	}
	envs, leftOut := sentry.Adapter{Org: org}.Parse(raw)
	if envs == nil {
		return nil, nil, leftOut
	}
	who, err := st.EnsureActor(ctx, store.Actor{Kind: "bot", DisplayName: sentry.Actor, Identities: map[string]string{"sentry": sentry.Actor}})
	if err != nil {
		return nil, nil, err
	}
	prefix := "sentry:" + org + "/" + project + "#"
	var skipped []error
	for _, env := range envs {
		if err := env.Validate(); err != nil {
			return results, leftOut, err
		}
		if len(env.Ref) <= len(prefix) || env.Ref[:len(prefix)] != prefix {
			skipped = append(skipped, fmt.Errorf("%s is not in project %s/%s, left out", env.Ref, org, project))
			continue
		}
		var p sentry.Payload
		if err := json.Unmarshal(env.Payload, &p); err != nil {
			return results, leftOut, fmt.Errorf("workitem: payload of %s: %w", env.Ref, err)
		}
		p.Count, p.LastSeen = 0, ""
		stored, err := json.Marshal(p)
		if err != nil {
			return results, leftOut, err
		}
		w, outcome, err := st.RecordSourced(ctx, store.Sourced{
			ProjectID:   projects[0].ID,
			Ref:         env.Ref,
			Title:       cleanText("Sentry "+p.ShortID+": "+p.Title, maxTitle),
			Payload:     stored,
			SourceEvent: env.Key,
			Kind:        "sentry",
			ActorID:     who.ID,
		})
		if errors.Is(err, store.ErrWrongProject) {
			skipped = append(skipped, fmt.Errorf("%s belongs to a work item of another project, left as it is", env.Ref))
			continue
		}
		if err != nil {
			return results, leftOut, fmt.Errorf("%s: %w", env.Ref, err)
		}
		results = append(results, SentryResult{
			Outcome: outcome, WorkItem: w.ID, Ref: env.Ref, ID: p.ID, ShortID: cleanText(p.ShortID, maxName),
			Level: cleanText(p.Level, maxName), Title: cleanText(p.Title, maxTitle), Repo: repo,
		})
	}
	return results, errors.Join(append(skipped, leftOut)...), nil
}
