# 0021. fix-issue routes to hidden workflows

- Status: accepted
- Date: 2026-11-25
- Supersedes: none
- Superseded by: none

## Context

`fix-issue` had one agent step with one prompt and one model for every
repository. A run on `awf-project/cli#455`, a repository I do not maintain,
ended `completed` with no pull request: the agent edited three files, left the
checkout uncompilable, wrote none of its three output files and stopped. The
push step then threw the edits away and exited 0, so nothing told anyone. The
prompt had contributed: its output files came last, and its rule "an open choice
means no code" contradicted an issue that mixed a settled defect and an open
design choice.

A repository I maintain and an upstream differ in what the agent should read
first, in the model worth spending, and in where the branch goes (ADR 0020).
awf has `call_workflow` and conditional `transitions`, but no way to hide a
workflow from the list.

## Decision

We keep `fix-issue` as the one entry point `awf-gh` dispatches, and make it a
router. It fetches the issue, asks GitHub whether the token can push to the
repository, and calls `_fix-issue-owned` on write access or
`_fix-issue-external` otherwise, then runs the shared `push` and `notify` steps.
A failed or unexpected answer counts as external, the path that never pushes to
the upstream directly.

The two children hold only the agent step, each with its own prompt and `model`
input. A leading underscore marks a workflow that only another workflow calls;
the dashboard hides such a workflow from its list unless the filter starts with
`_`. Each child is its own model role, so the Config view sets them apart.

The prompt writes `answer.md` and `worklog.md` right after the proof, fixes only
what the issue settles and drafts the open part as an ADR, and must leave the
checkout compiling or restore it. The `push` step now fails the run, keeps the
worktree and comments `answer.md` on the issue when the agent left edits but no
`fix.md`.

## Consequences

- An upstream issue gets its own prompt and model without touching the owned
  path, and awf-gh configuration does not change.
- Most steps stay in one file. Only the agent step is duplicated, and the two
  prompts share their text except for a contributor-rules paragraph.
- The underscore is a convention. awf and its API still list and can run a
  hidden workflow; only the dashboard hides it.
- `fix-issue` no longer has a `model` input. A model override for one run now
  goes to a child, and a dashboard-started run of `fix-issue` takes the saved
  defaults.
- The dashboard's step names, history and cost for a run with a sub-workflow
  have only been checked against fixtures, not against a real run.

## Alternatives

- **Workflow packs.** They install from GitHub Releases and run as
  `pack/workflow`, which needs a release cycle for a purely local split, and
  they do not hide anything.
- **Templates (`use_template`).** They would share the steps, but a first test
  did not validate and I did not pursue it.
- **One workflow with a prompt that branches on the repository.** The prompt
  stays one text and the model stays one setting, which is the problem.
