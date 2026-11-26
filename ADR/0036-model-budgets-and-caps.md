# 0036. Model spending has a payer, a budget and a hard cap

- Status: proposed
- Date: 2026-11-26
- Supersedes: none
- Superseded by: none

## Context

Run cost already comes from awf ([0008](0008-run-cost-comes-from-awf.md)): since awf v0.12.0 the
provider price of an agent step travels with the run as `cost_usd`, and `cmd/dashboard/cost.go`
and `/api/usage` already sum it per run, per pull request and per workflow. Nothing limits it. A
large pull request, a loop between review and fix ([0010](0010-review-fix-loop.md)) or a busy
repository can spend without bound.

What is built that a cap can lean on:

- The numbers: `/api/usage` reads `state/logs/index.jsonl` and each run's log.
- A pre-publication precedent: the circuit breaker (`docs/breaker.md`) writes a file under
  `STATE_DIR/breaker/` and `bin/publish-gate` reads it. A cap can use the same shape: a file
  that a shell step checks.
- The control-plane database (`internal/store/store.go`) has no spend table, no payer and no
  budget. It has `workspace` and `actor`, which is enough to name a payer later.

What is not built: any check before dispatch, any per-run abort, any payer. Models are also tied
to trust: a client may impose a provider ([0032](0032-trust-levels-per-repository.md)). See use
cases A4, B6, D1, D6 and the "cost runaway" edge case.

## Decision

Open. Proposed shape: a budget is a number per period attached to a repository, a workspace or a
person, with a payer named beside it. A soft threshold alerts. A hard cap stops new tasks and says
so on the pull request, because an alert alone does not stop a loop. A cost per task cap aborts a
single run cleanly.

### Recommendation

Start with one thing: a daily hard cap in USD per repository, and nothing about payers.

Smallest first step: `bin/budget check OWNER/REPO` that sums today's `cost_usd` for that repository
from `state/logs/index.jsonl` and exits 3 over the cap. `bin/tend` and the watcher call it before
posting a task, and `review-pr`'s fix loop calls it before each round. The cap is a `budget` key
in `repos.yaml` (`daily_usd`). A refusal is a note, written like the gate's notes, so the dashboard
shows it. No store table until a second period or a payer is needed.

### Options

| Option | What it costs | What it rules out |
| --- | --- | --- |
| A. Daily USD cap per repository, a shell check (recommended) | one script, one key, reads existing logs | a payer, a monthly view, a per-person cap |
| B. Per-run cap only | needs awf to abort a run mid-step | stopping a loop made of many cheap runs |
| C. Store-backed budgets with payer | a migration, a spend table, dashboard forms | shipping soon |
| D. Alerts only | a threshold in the dashboard | stopping a runaway |

### Questions for the owner

1. Is a daily cap per repository the right first unit (pick: daily per repository, or one daily cap
   for the whole dot)?
2. When the cap is hit, does the dot stop with a note only, or also comment on the open pull
   request (pick: note only, or note and comment)?
3. May the payer wait until the first shared dot (yes or no)?

## Consequences

Needs a check before dispatch and a clear message when a task is refused. It makes team use
([0035](0035-a-dot-is-an-engine-instance-with-a-policy-profile.md)) possible, and rules out
unmetered shared use. Reading logs gives a number that lags a run by its own duration, so one run
can overshoot the cap.

## Alternatives

Alerts only: cheap, does not stop runaway. Provider-side limits only: coarse, per key and not
per repository.
