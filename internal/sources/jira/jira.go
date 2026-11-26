// Package jira is the adapter for Jira issues (ADR 0023, ADR 0039). It reads an issue
// webhook payload (created, updated, comment added) or the JSON of the search-issues API
// and emits envelopes. It never talks to Jira.
//
// Everything a person typed in Jira (summary, description, comments, labels) is data
// from a third party, possibly hostile. It is carried in the payload and flattened to
// plain text, and nothing here reads it for references, repositories or commands. The
// reference comes from the structured issue key, the project is the prefix of that key,
// and the repository comes only from the Projects table.
package jira

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"opencode-dots/internal/sources"
)

// Envelope kinds. A creation and an update are different kinds, so they never share a key.
const (
	KindCreated = "jira.issue.created"
	KindUpdated = "jira.issue.updated"
	// KindSeen is what a poll yields: the search API cannot say whether the issue is new.
	KindSeen    = "jira.issue.seen"
	KindComment = "jira.comment.added"
)

// Actor is the fallback when the source names nobody.
const Actor = "jira"

// MaxText caps each flattened text field, in bytes.
const MaxText = 32 << 10

// Adapter parses Jira payloads. The zero value maps no project, so every issue is
// diagnosis-only.
type Adapter struct {
	Projects Projects
	// Now stands in for the event time when the source carries none.
	Now func() time.Time
}

var _ sources.Adapter = Adapter{}

// Tracker implements sources.Adapter.
func (Adapter) Tracker() sources.Tracker { return sources.Jira }

// Payload is the envelope payload. Text fields are data from a third party.
type Payload struct {
	Key     string `json:"key"`
	Project string `json:"project"`
	// Repository is owner/repo from the Projects table. Empty when DiagnosisOnly.
	Repository    string `json:"repository,omitempty"`
	DiagnosisOnly bool   `json:"diagnosis_only"`

	Summary     string   `json:"summary,omitempty"`
	Description string   `json:"description,omitempty"`
	IssueType   string   `json:"issue_type,omitempty"`
	Status      string   `json:"status,omitempty"`
	Priority    string   `json:"priority,omitempty"`
	Labels      []string `json:"labels,omitempty"`
	Created     string   `json:"created,omitempty"`
	Updated     string   `json:"updated,omitempty"`
	// Changed lists the field names of a webhook changelog.
	Changed []string `json:"changed,omitempty"`
	// Truncated is true when a text field was cut at MaxText.
	Truncated bool `json:"truncated,omitempty"`

	Comment *Comment `json:"comment,omitempty"`
}

// Comment is one comment, flattened to text.
type Comment struct {
	ID      string `json:"id"`
	Author  string `json:"author,omitempty"`
	Body    string `json:"body"`
	Created string `json:"created,omitempty"`
}

type person struct {
	AccountID   string `json:"accountId"`
	DisplayName string `json:"displayName"`
}

func (p *person) name() string {
	if p == nil {
		return ""
	}
	if p.AccountID != "" {
		return p.AccountID
	}
	return p.DisplayName
}

type named struct {
	Name string `json:"name"`
}

type rawComment struct {
	ID      string          `json:"id"`
	Author  *person         `json:"author"`
	Body    json.RawMessage `json:"body"`
	Created string          `json:"created"`
}

type rawIssue struct {
	Key    string `json:"key"`
	Fields struct {
		Summary     string          `json:"summary"`
		Description json.RawMessage `json:"description"`
		IssueType   *named          `json:"issuetype"`
		Status      *named          `json:"status"`
		Priority    *named          `json:"priority"`
		Labels      []string        `json:"labels"`
		Reporter    *person         `json:"reporter"`
		Created     string          `json:"created"`
		Updated     string          `json:"updated"`
		Comment     struct {
			Comments []rawComment `json:"comments"`
		} `json:"comment"`
	} `json:"fields"`
}

type document struct {
	Timestamp    json.Number `json:"timestamp"`
	WebhookEvent string      `json:"webhookEvent"`
	User         *person     `json:"user"`
	Issue        *rawIssue   `json:"issue"`
	Comment      *rawComment `json:"comment"`
	Changelog    *struct {
		ID    string `json:"id"`
		Items []struct {
			Field string `json:"field"`
		} `json:"items"`
	} `json:"changelog"`
	Issues []rawIssue `json:"issues"`
}

// Parse implements sources.Adapter. A webhook yields one envelope. A search yields one
// "seen" envelope per issue plus one comment envelope per comment it carries. An issue
// without a valid key is left out and named in the error, the others are returned.
func (a Adapter) Parse(raw []byte) ([]sources.Envelope, error) {
	var d document
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, fmt.Errorf("jira: not jira json: %w", err)
	}
	switch {
	case d.Issues != nil:
		return a.search(d)
	case d.WebhookEvent != "":
		return a.webhook(d)
	}
	return nil, errors.New("jira: neither a webhook payload nor a search result")
}

func (a Adapter) webhook(d document) ([]sources.Envelope, error) {
	if d.Issue == nil {
		return nil, fmt.Errorf("jira: %s has no issue", d.WebhookEvent)
	}
	ref, err := issueRef(d.Issue)
	if err != nil {
		return nil, err
	}
	when := a.now()
	if ms, err := d.Timestamp.Int64(); err == nil && ms > 0 {
		when = time.UnixMilli(ms).UTC()
	}
	actor := orDefault(d.User.name(), Actor)
	p := a.payload(d.Issue)
	var kind, key string
	switch d.WebhookEvent {
	case "jira:issue_created":
		kind = KindCreated
		key = sources.IdempotencyKey(ref, kind)
	case "jira:issue_updated":
		kind = KindUpdated
		id := ""
		if d.Changelog != nil {
			id = d.Changelog.ID
			for _, it := range d.Changelog.Items {
				p.Changed = append(p.Changed, it.Field)
			}
		}
		// The changelog id names the change; the issue's updated stamp names the state.
		key = sources.IdempotencyKey(ref, kind, id, d.Issue.Fields.Updated)
	case "comment_created":
		if d.Comment == nil || d.Comment.ID == "" {
			return nil, fmt.Errorf("jira: %s: comment event without a comment id", ref)
		}
		kind = KindComment
		p.Comment = commentOf(*d.Comment, &p)
		actor = orDefault(d.Comment.Author.name(), actor)
		key = sources.IdempotencyKey(ref, kind, d.Comment.ID)
	default:
		return nil, fmt.Errorf("jira: %s: unsupported event %q", ref, d.WebhookEvent)
	}
	return one(ref, kind, actor, when, p, key)
}

func (a Adapter) search(d document) ([]sources.Envelope, error) {
	var out []sources.Envelope
	var errs []error
	for i := range d.Issues {
		is := &d.Issues[i]
		ref, err := issueRef(is)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		// The updated stamp is part of the key: an unchanged issue repeats its key on the
		// next poll, an issue edited since gets a new one.
		envs, err := one(ref, KindSeen, orDefault(is.Fields.Reporter.name(), Actor),
			a.parseTime(is.Fields.Updated), a.payload(is),
			sources.IdempotencyKey(ref, KindSeen, is.Fields.Updated))
		if err != nil {
			errs = append(errs, err)
			continue
		}
		out = append(out, envs...)
		for _, c := range is.Fields.Comment.Comments {
			if c.ID == "" {
				errs = append(errs, fmt.Errorf("jira: %s: comment without an id left out", ref))
				continue
			}
			cp := a.payload(is)
			cp.Comment = commentOf(c, &cp)
			ce, err := one(ref, KindComment, orDefault(c.Author.name(), Actor), a.parseTime(c.Created), cp,
				sources.IdempotencyKey(ref, KindComment, c.ID))
			if err != nil {
				errs = append(errs, err)
				continue
			}
			out = append(out, ce...)
		}
	}
	return out, errors.Join(errs...)
}

// issueRef builds the reference from the structured key only. A key that fails the
// grammar is an error: nothing is guessed from the summary or the description.
func issueRef(is *rawIssue) (string, error) {
	r, err := sources.NewRef(sources.Jira, is.Key)
	if err != nil {
		return "", fmt.Errorf("jira: issue %q has no valid key", is.Key)
	}
	return r.String(), nil
}

func (a Adapter) payload(is *rawIssue) Payload {
	f := is.Fields
	project := ProjectOf(is.Key)
	p := Payload{
		Key: is.Key, Project: project,
		IssueType: nameOf(f.IssueType), Status: nameOf(f.Status), Priority: nameOf(f.Priority),
		Labels: f.Labels, Created: f.Created, Updated: f.Updated,
	}
	p.Repository = a.Projects.Repository(project)
	p.DiagnosisOnly = p.Repository == ""
	var cut bool
	p.Summary, cut = clip(f.Summary)
	p.Truncated = cut
	desc, cut := clip(FlattenText(f.Description))
	p.Description, p.Truncated = desc, p.Truncated || cut
	return p
}

func commentOf(c rawComment, p *Payload) *Comment {
	body, cut := clip(FlattenText(c.Body))
	p.Truncated = p.Truncated || cut
	return &Comment{ID: c.ID, Author: c.Author.name(), Body: body, Created: c.Created}
}

func one(ref, kind, actor string, when time.Time, p Payload, key string) ([]sources.Envelope, error) {
	body, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	return []sources.Envelope{{Ref: ref, Kind: kind, Actor: actor, Time: when, Payload: body, Key: key}}, nil
}

// Jira writes 2024-03-05T10:15:30.123+0000; RFC 3339 is accepted too.
func (a Adapter) parseTime(s string) time.Time {
	for _, layout := range []string{"2006-01-02T15:04:05.000-0700", time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return a.now()
}

func (a Adapter) now() time.Time {
	if a.Now != nil {
		return a.Now().UTC()
	}
	return time.Now().UTC()
}

func nameOf(n *named) string {
	if n == nil {
		return ""
	}
	return n.Name
}

func orDefault(s, d string) string {
	if strings.TrimSpace(s) == "" {
		return d
	}
	return s
}

func clip(s string) (string, bool) {
	if len(s) <= MaxText {
		return s, false
	}
	cut := MaxText
	for cut > 0 && s[cut]&0xC0 == 0x80 { // stay on a rune boundary
		cut--
	}
	return s[:cut], true
}
