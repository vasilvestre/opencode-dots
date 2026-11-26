# 0036. Model spending has a payer, a budget and a hard cap

- Status: proposed
- Date: 2026-11-26
- Supersedes: none
- Superseded by: none

## Context

Run cost already comes from awf ([0008](0008-run-cost-comes-from-awf.md)) and the dashboard
shows it. Nothing limits it. A large pull request, a loop between review and fix
([0010](0010-review-fix-loop.md)) or a busy repository can spend without bound, and on a
shared dot nobody knows whose money it is. Models are also tied to trust: a client may impose
a provider ([0032](0032-trust-levels-per-repository.md)). See use cases A4, B6, D1, D6, and the
"cost runaway" edge case.

## Decision

Open. Proposed shape, to be argued: a budget is a number per period attached to a repository,
a workspace or a person, with a payer named beside it. A soft threshold alerts. A hard cap
stops new tasks and says so on the pull request, because an alert alone does not stop a loop.
A cost per task cap aborts a single run cleanly.

## Consequences

Needs a store for spend by period (the control-plane database,
[0024](0024-control-plane-database.md)), a check before dispatch, and a clear message when a
task is refused. It makes team use ([0035](0035-team-shapes-one-dot-per-person-or-team.md))
possible, and rules out unmetered shared use.

## Alternatives

Alerts only: cheap, does not stop runaway. Provider-side limits only: coarse, per key and not
per repository.
