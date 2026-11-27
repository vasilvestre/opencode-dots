# 0041. Linking work items across sources is a proposal; merging stays explicit

- Status: proposed
- Date: 2026-11-26
- Supersedes: none
- Superseded by: none

## Context

A GitHub issue, a Jira ticket and a Sentry issue often describe the same defect. Seeing it is worth
a lot: one pull request instead of three, one context, no contradicting fixes. A model with a large
context can judge it. See use case F4, which `docs/use-cases.md` lists as "ADR only".

[0023](0023-tracker-independent-work-item.md) forbids two things this needs: it says a reference
belongs to one work item and that **merging is an explicit action, never an inference**, and that we
never read free text to attach references.

What is built:

- `work_item_ref` in `internal/store/store.go` has `ref` as UNIQUE, so one reference belongs to one
  work item by constraint, not by convention. A link therefore cannot be a second reference row.
- Every row records `attached_by` (an actor), so a proposal by the dot and a confirmation by a
  person are distinguishable.
- Two adapters that can produce a cluster exist as parsers (Sentry, Jira in `internal/sources`) and
  are not wired, so there are no real items to measure a confidence on.
- The dashboard already has a `propose` pattern: held records with a release button
  (`/api/autonomy/release`, `docs/publish-held.md`). A link proposal can reuse the same shape.
- There is no link table, no proposal record and no merge operation.

## Decision

We keep 0023 and add a step before the explicit action. A **link proposal** is the output of a
workflow: it names two work items, a confidence, a reason and the evidence it used. The proposal is
data. It changes nothing by itself.

A proposal becomes a merge only through an explicit action: a person confirms it in the dashboard
(`propose`), or a rule applies it above a confidence the owner set (`auto`,
[0040](0040-classification-and-autonomy-ceiling.md)). A merge can be split again, and the history
keeps both.

A link never carries content across a trust boundary ([0032](0032-trust-levels-per-repository.md)):
when the two items sit on different sides, the link holds references and states only.

Once linked, one pull request may close the cluster and every source gets its own write-back.

### Recommendation

Ship `propose` only. Never `auto` until real data exists.

Smallest first step, and not before two sources are wired (see
[0039](0039-work-sources-are-adapters.md)): a `link_proposal` table (two work item ids, confidence,
reason, state `open`, `accepted`, `declined`, `proposed_by`) and a dashboard list that reuses the held
records page. Accepting calls one store operation that moves the references of one item onto the
other, with the old item kept as `merged`. A workflow writes the proposal as a file and a shell
step inserts it, as ADR 0002 requires.

### First step built

The record stays `proposed`. Option A's smallest step exists, and nothing changes for a deployment
that does not use it: migration 6 (`link_proposal`, and `merged_into` on `work_item`), the store
operations `ProposeLink`, `ListLinkProposals`, `DeclineLink` and `AcceptLink`, `bin/repos db link`,
`GET /api/links` with accept and decline endpoints, and a Links page. Accepting refuses, leaving the
proposal open, across workspaces and across trust levels: a link that holds references and state only
(question 3) is not built, so the answer in code is "refuse". Splitting is built too (migration 7, the
store operation `SplitLink`, `bin/repos db link split` and a Split button): it moves exactly the references
that were moved, from the kept item back to the merged one, and refuses when anything has changed since.
A hidden workflow, `_propose-links`, now writes proposals: a shell step picks candidate pairs from
structured fields only, an agent rates each pair, a shell step validates and inserts. It never accepts a
link. Its agent step has only run against a stub, and nothing schedules it. See `docs/links.md`.

### Options

| Option | What it costs | What it rules out |
| --- | --- | --- |
| A. `propose` only, a person accepts (recommended) | one table, one store operation, one list | automatic clustering |
| B. Also `auto` above a threshold | A plus a measured confidence | doing it before real data |
| C. Structured fields only (branch, issue link) | no model, no table | most real cases, F4 itself |
| D. Merge on inference | the least work | 0023 and the trust boundary |

### Questions for the owner

1. Is `propose` with a person's confirmation the only mode for now (yes or no)?
2. Should this wait until two sources are wired, since there is nothing to link yet (yes or no)?
3. When two items sit on different trust sides, may the link store references only (yes, or pick:
   refuse the link)?

## Consequences

Easier: a cluster of alerts becomes one piece of work. The dashboard can show "3 reports, one cause".

Harder: a wrong merge sends a fix against the wrong problem, so confidence must be measured on real
items before any rule applies it alone. The free-text rule of 0023 is not bypassed: the model reads
text to propose, and a person or a threshold decides. The UNIQUE constraint on `ref` means a merge
moves rows, which the store must do in one transaction.

## Alternatives

Attach by shared structured fields only (a branch, an issue link): safe, but misses most of the cases
that matter, since a Sentry issue has no link to a ticket. Merge on inference: contradicts 0023 and
can leak context across clients.
