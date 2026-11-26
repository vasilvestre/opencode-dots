# 0039. Work sources are adapters that feed work items

- Status: proposed
- Date: 2026-11-26
- Supersedes: none
- Superseded by: none

## Context

[0023](0023-tracker-independent-work-item.md) made the work item independent of the tracker and
named GitHub as the first adapter, Jira as a later one. The work the dot should take in is wider
than issues and pull requests: vulnerable dependencies, production errors, tickets in a company
tracker such as the Jira agent's, red checks and stale branches. Each arrives by its own channel
but wants the same treatment: a work item, a classification
([0040](0040-classification-and-autonomy-ceiling.md)), a fixed workflow, a shell step that writes
back. See use cases F1 to F3.

What is built (the inbound half only, see `docs/sources.md`):

- `internal/sources` holds the envelope (`ref`, `kind`, `actor`, `time`, `payload`, `key`), the
  reference grammar (`github`, `branch`, `jira`, `sentry`, `advisory`) and `IdempotencyKey`.
- Three adapters: advisory (`composer audit --format=json`), Sentry (`docs/sources-sentry.md`) and
  Jira (`docs/sources-tracker.md`). Each is a parser and its tests. Nothing polls, no webhook
  endpoint exists, nothing is written back, and nothing calls the package.
- Rules already hold in code and fixtures: hostile text stays inert, keys come from structured
  fields, repeated polls give the same key.
- The adapters disagree on one point of this record. Jira has a `Projects` table from project key
  to `owner/repo`, and an unmapped project carries `diagnosis_only: true` with no `repository`.
  Sentry has no such table: its reference carries the project slug and the adapter does not say
  which repository it feeds. The advisory adapter has no repository at all, since `composer audit`
  runs inside one. So the "scoped" rule below is true for Jira and absent for the other two.

## Decision

We treat every source as an **adapter** with the two jobs of 0023: inbound, it turns an event into
the common envelope; outbound, a shell step posts the result back. New reference kinds follow
`tracker:key`. The existing GitHub watcher is the first adapter and is not rewritten.

Rules every adapter keeps:

- **Text is data.** A Sentry message, a stack trace, a ticket body or an advisory summary is
  attacker-influenced input, never an instruction.
- **Idempotent inbound.** An adapter gives each event a stable key, so a repeated poll or webhook
  creates no second work item.
- **Opt-in.** A source is polled only where the owner turned it on
  ([0030](0030-watcher-is-opt-in.md)).
- **Scoped.** A source feeds the repositories that name it in `repos.yaml`; an unmapped item stays
  diagnosis-only. The envelope carries this the way Jira does: a `repository` and a
  `diagnosis_only` payload field, set by the caller's mapping, never read from the item's text.

An adapter is a separate unit from the workflows, so it can ship with the engine or be kept apart
([0035](0035-a-dot-is-an-engine-instance-with-a-policy-profile.md)).

### Recommendation

Make the Jira shape the contract: give Sentry a `Projects` table (project slug to `owner/repo`) with
the same `repository` and `diagnosis_only` payload fields, and have the advisory adapter take its
repository from the caller. Then wire one adapter end to end before the others: advisory first,
because it needs no credentials and no network.

Smallest first step: add the mapping to the Sentry adapter and a shared repository and
diagnosis-only helper in `internal/sources`, then a `bin/advisory-poll` shell step that runs
`composer audit`, feeds the adapter and records each envelope in the store (the idempotency key as
the source event). Do not add polling for Sentry or Jira yet.

### Options

| Option | What it costs | What it rules out |
| --- | --- | --- |
| A. Common mapping, wire advisory first (recommended) | a small refactor, one script | Sentry and Jira going live first |
| B. Wire Jira first | credentials, JQL, a webhook or poll | a no-credential first proof |
| C. Leave the adapters as they are | nothing | one rule for "unmapped"; a clean dashboard list |
| D. One workflow per source | per-source state | one classification and one dashboard list |

### Questions for the owner

1. May advisory be the first source wired end to end (yes, or pick Sentry or Jira)?
2. Should an unmapped Sentry project be diagnosis-only, like Jira, or ignored (pick one)?
3. Is the mapping kept in `repos.yaml` next to each repository (yes, or pick: a separate file)?

## Consequences

Easier: a CVE or a Sentry issue goes through the same workflows as an issue, and the dashboard shows
one list of work.

Harder: each adapter needs credentials, rate limits and a way to be tested without the real service
(the fixtures already do this for parsing). Sentry and Jira bring data of third parties, which meets
the trust levels of [0032](0032-trust-levels-per-repository.md).

## Alternatives

One workflow per source with its own state: duplicates classification, memory and dashboard per
source. Reading everything as GitHub issues through bridges: loses the source's own fields and
transitions.
