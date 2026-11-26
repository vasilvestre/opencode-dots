# Sentry source adapter

`internal/sources/sentry` turns Sentry issue data into `sources.Envelope` values
(ADR 0023, ADR 0039). It is a parser and its tests: it never calls Sentry, polls
nothing and writes nothing back.

## Reference

`sentry:org/project#id`, for example `sentry:acme/web#4711`. The three parts come only
from structured fields:

- `org` is set by the caller (`Adapter{Org: "acme"}`): neither a webhook issue nor a
  list item carries the organization slug.
- `project` is the issue's `project.slug`.
- `id` is the numeric issue id (the short id such as `WEB-1A` is also valid in the
  grammar, but the adapter uses the numeric id and carries the short id as data).

The reference is checked with `sources.NewRef`. Slugs must be lowercase. An item that
cannot form a valid reference is left out.

## Inputs and kinds

`Parse` takes the raw bytes. A JSON array is a list-issues response, an object is a
webhook body.

| Input | Kind | Envelope time |
| --- | --- | --- |
| webhook `created` | `sentry.issue.created` | `firstSeen` |
| webhook `regression` | `sentry.issue.regression` | `lastSeen` |
| webhook `resolved` | `sentry.issue.resolved` | `lastSeen` |
| list item | `sentry.issue.listed` | `lastSeen` |

Any other webhook action returns an error and no envelope. The actor is always
`sentry`.

## Idempotency key

- list item and `created`: `IdempotencyKey(ref, kind)`. A repeated poll, with more
  events or a later `lastSeen`, gives the same key, so nothing new reaches the store.
- `regression` and `resolved`: `IdempotencyKey(ref, kind, lastSeen)`. A redelivery of
  the same webhook keeps its key, and a later regression of the same issue is new.

## Payload

| Field | Source |
| --- | --- |
| `id`, `short_id` | `id`, `shortId` |
| `title`, `culprit` | `title`, `culprit` |
| `level`, `status` | `level`, `status` |
| `first_seen`, `last_seen` | `firstSeen`, `lastSeen` |
| `count` | `count` (a string in the API; 0 if unreadable) |
| `permalink` | `permalink`, kept only when it starts with `https://` |
| `frames` | webhook only, when the body carries an exception: up to 5 frames, innermost first, each with `file`, `function`, `line`, `in_app` |

The list-issues API has no stack frames, so a listed envelope has none.

## What is data

Everything in the payload except the reference. Sentry shows what applications report,
so titles, culprits, messages, tags and stack frames are attacker-influenced. The
adapter:

- never parses them for references, commands, paths or URLs;
- strips control characters and cuts each string to 512 runes;
- drops a permalink that is not `https://`;
- builds the reference and the key from structured fields only.

A consumer must put them in the data part of a prompt, quoted and labeled untrusted,
and never in the instruction part. The fixtures include a title and a culprit that try
to give instructions: they stay inert strings.

## Calling it

A poller reads the list-issues JSON itself, then:

```go
envs, err := sentry.Adapter{Org: "acme"}.Parse(body)
```

A webhook handler does the same with the request body, after checking the signature
itself. `err` may come with valid envelopes (a bad item in a list): store the envelopes,
log the error. Pass `Envelope.Key` to the store as the source event so a repeat is a
no-op.

## Not done

- no polling and no HTTP client: nothing fetches the list, nothing pages through it;
- no webhook endpoint and no signature check;
- no write-back (comment, resolve, assign): that stays in shell steps (ADR 0002);
- no actions other than `created`, `regression` and `resolved`;
- no stack frames from the list API, no event fetch.
