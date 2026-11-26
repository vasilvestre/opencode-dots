# Tracker sources: Jira

`internal/sources/jira` turns Jira data into the envelopes of `docs/sources.md`. It is a
package and its tests only: no polling, no webhook listener, no network.

## Input

`Adapter.Parse(raw)` takes one of two bodies and tells them apart itself:

- an issue webhook payload (`webhookEvent`): `jira:issue_created`, `jira:issue_updated`,
  `comment_created`. Any other event is an error.
- the search-issues API JSON (`issues` array). Comments ride along when the search asked
  for the `comment` field.

An unreadable body, or one that is neither, is an error and yields nothing. In a search,
an issue whose key is invalid is left out and named in the error; the others are returned.

## Reference

`jira:ABC-12`, read from the structured `issue.key` only and validated by `ParseRef`.
The project key is the prefix of the issue key (`ABC`), not `fields.project.key`.

## Kinds and keys

| Kind | Source | Key parts |
| --- | --- | --- |
| `jira.issue.created` | webhook | ref, kind |
| `jira.issue.updated` | webhook | ref, kind, changelog id, issue `updated` stamp |
| `jira.comment.added` | webhook or search | ref, kind, comment id |
| `jira.issue.seen` | search | ref, kind, issue `updated` stamp |

A creation and an update have different kinds and keys. A repeated poll of an unchanged
issue yields the same keys, so the store sees nothing new; an edit changes the `updated`
stamp and so the key of the `seen` envelope, while its comments keep theirs. A search
cannot say whether an issue is new, hence `seen`; a webhook `created` and a poll `seen`
of the same issue are two different events.

Actor is the account id of the webhook user, comment author or reporter, then the display
name, then `jira`.

## Project mapping

`Projects` is a table from project key to `owner/repo`, held by the caller:

```go
jira.Adapter{Projects: jira.Projects{"ABC": "acme/shop"}}
```

An unmapped project is diagnosis-only: the payload has `diagnosis_only: true` and no
`repository`. The zero `Adapter` maps nothing. A malformed entry counts as unmapped
(`Projects.Validate` reports it at load time). The adapter never derives a repository
from issue text, labels or links.

## Payload

`key`, `project`, `repository`, `diagnosis_only`, `summary`, `description`, `issue_type`,
`status`, `priority`, `labels`, `created`, `updated`, `changed` (field names of the
changelog), `truncated`, and for a comment event `comment` (`id`, `author`, `body`,
`created`).

Text is flattened to plain text. A description is a string or an Atlassian document;
the document is walked for text, mentions and hard breaks, with blocks ending in a line
break. Marks, links, media and anything unknown are dropped, and nesting past 32 levels
is ignored. Each text field is cut at 32 KiB and `truncated` says so.

## Data rules

Summary, description, comments and labels are written by anyone who can touch the
project, so they are attacker-influenced data.

- No code in the adapter reads them for instructions, references, repositories or
  commands. The hostile fixture holds a prompt injection that names another issue, a
  pull request and a repository; none of it reaches `ref` or `repository`.
- A consumer must treat them as data too: quote them, never follow them, never run them
  (same rule as PR text in `AGENTS.md`).
- Flattening is not sanitising. The text may still contain markup, URLs and control
  characters.

## Not done

- No polling and no webhook endpoint: the caller supplies the bytes.
- No write-back: nothing comments on or transitions a Jira issue. An internal note on
  the issue is a shell step, later (ADR 0002).
- No authentication, pagination or JQL; no attachments, links between issues, sub-tasks
  or sprint data.
- No loading of the mapping from `repos.yaml`; the table is built by the caller.
- Jira Server webhooks that use the v2 wiki markup are read as plain text, not parsed.
