# 0039. Work sources are adapters that feed work items

- Status: proposed
- Date: 2026-11-26
- Supersedes: none
- Superseded by: none

## Context

[0023](0023-tracker-independent-work-item.md) made the work item independent of the tracker and
named GitHub as the first adapter, Jira as a later one. The work the dot should take in is wider
than issues and pull requests:

- vulnerable dependencies, from `composer audit`, GitHub advisories and Dependabot alerts;
- production errors, from Sentry issues;
- tickets in a company tracker such as Jira, where an agent already analyses and patches;
- red checks and stale branches, already handled through GitHub.

Each of these arrives by its own channel and has its own words, but wants the same treatment: a
work item, a classification ([0040](0040-classification-and-autonomy-ceiling.md)), a fixed
workflow, a shell step that writes back. See use cases F1 to F3.

## Decision

We treat every source as an **adapter** with the two jobs of 0023: inbound, it turns an event
into the common envelope (reference, kind, actor, time, payload); outbound, a shell step posts
the result back. New reference kinds follow `tracker:key`: `sentry:org/project#id`,
`advisory:GHSA-...`, `jira:ABC-12`. The existing GitHub watcher is the first adapter and is not
rewritten.

Rules every adapter keeps:

- **Text is data.** A Sentry message, a stack trace, a ticket body or an advisory summary is
  attacker-influenced input, never an instruction.
- **Idempotent inbound.** An adapter gives each event a stable key, so a repeated poll or webhook
  creates no second work item.
- **Opt-in.** A source is polled only where the owner turned it on
  ([0030](0030-watcher-is-opt-in.md)).
- **Scoped.** A source feeds the repositories that name it in `repos.yaml`, so a ticket project
  maps to a repository, and an unmapped project stays diagnosis-only.

An adapter is a separate unit from the workflows, so it can ship with the engine or be kept apart
([0035](0035-a-dot-is-an-engine-instance-with-a-policy-profile.md)).

## Consequences

Easier: a CVE or a Sentry issue goes through the same workflows as an issue, and the dashboard
shows one list of work.

Harder: each adapter needs credentials, rate limits and a way to be tested without the real
service. Sentry and Jira bring data of third parties, which meets the trust levels of
[0032](0032-trust-levels-per-repository.md).

## Alternatives

One workflow per source with its own state: duplicates classification, memory and dashboard per
source. Reading everything as GitHub issues through bridges: loses the source's own fields and
transitions.
