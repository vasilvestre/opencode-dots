# 0041. Linking work items across sources is a proposal; merging stays explicit

- Status: proposed
- Date: 2026-11-26
- Supersedes: none
- Superseded by: none

## Context

A GitHub issue, a Jira ticket and a Sentry issue often describe the same defect. Seeing it is
worth a lot: one pull request instead of three, one context, no contradicting fixes. A model with
a large context can judge it. See use case F4.

[0023](0023-tracker-independent-work-item.md) forbids two things this needs: it says a reference
belongs to one work item and that **merging is an explicit action, never an inference**, and that
we never read free text to attach references.

## Decision

We keep 0023 and add a step before the explicit action. A **link proposal** is the output of a
workflow: it names two work items, a confidence, a reason and the evidence it used. The proposal
is data. It changes nothing by itself.

A proposal becomes a merge only through an explicit action: a person confirms it in the
dashboard (`propose`), or a rule applies it above a confidence the owner set (`auto`,
[0040](0040-classification-and-autonomy-ceiling.md)). A merge can be split again, and the history
keeps both.

A link never carries content across a trust boundary
([0032](0032-trust-levels-per-repository.md)): when the two items sit on different sides, the link
holds references and states only.

Once linked, one pull request may close the cluster and every source gets its own write-back.

## Consequences

Easier: a cluster of alerts becomes one piece of work. The dashboard can show "3 reports, one
cause".

Harder: a wrong merge sends a fix against the wrong problem, so confidence must be measured on
real items before any rule applies it alone. The free-text rule of 0023 is not bypassed: the model
reads text to propose, and a person or a threshold decides.

## Alternatives

Attach by shared structured fields only (a branch, an issue link): safe, but misses most of the
cases that matter, since a Sentry issue has no link to a ticket. Merge on inference: contradicts
0023 and can leak context across clients.
