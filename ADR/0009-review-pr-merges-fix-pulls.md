# 0009. review-pr merges its own fix-issue pull requests

- Status: accepted
- Date: 2026-11-23
- Supersedes: none
- Superseded by: none

## Context

`fix-issue` turns a labeled issue into one pull request and never merges it.
`review-pr` writes a one-word verdict (`merge` or `hold`) and posts the review,
but it never merges either. Only `tend-repo` merges, and only the pull request
it opens itself.

The user wants the dot to merge a pull request that `fix-issue` opened, once
`review-pr` judges it ready, without turning on the repository's auto-merge
setting. `allow_auto_merge` is false on `vasilvestre/opencode-dream-memory`, and
`gh pr merge --auto` needs that setting. `awf-gh` sends only `repo`, `pr`, `sha`
and `event` to a workflow, so a per-repository `auto_merge` flag cannot reach
`review-pr` without changing `awf-gh` and rebuilding the image.

## Decision

We merge inside `review-pr`, in a new `automerge` state after `post`. It merges
only when four conditions hold: the verdict is `merge`, the head branch starts
with `fix/` (the branch `fix-issue` pushes), the pull request body says
`Closes #N`, and `gh pr checks --watch` reports every check green. The merge is
a plain `gh pr merge --squash --delete-branch`, so it needs no repository
setting. The checks are watched in the same run, because nothing fires when they
turn green after `pr-opened`.

## Consequences

A fix-issue pull request reaches `main` without a manual merge and without the
repository auto-merge setting. A human pull request is never merged by this
path: its branch does not start with `fix/`. A repo with no CI never
auto-merges, because `gh pr checks` fails when no check is reported; that repo
keeps the manual merge.

The run now waits for CI, up to the step timeout, so a slow pipeline keeps the
run open longer. If branch protection requires an approving review, the bot's
review comment is not an approval and the merge fails; the pull request stays
open and the failure is logged.

## Alternatives

- **Turn on `allow_auto_merge` and use `gh pr merge --auto`.** The user ruled it
  out, and it changes a repository setting rather than the workflow.
- **Pass `auto_merge` per repository through `awf-gh`.** It needs a new field in
  `awf-gh`'s config, a change to `dispatch`, and an image rebuild. It is the
  right shape if more per-repository options appear, but it is more moving parts
  than one branch-prefix test for this one case.
- **Merge every pull request whose verdict is `merge`.** It would also merge
  human pull requests and dependabot bumps. The `fix/` branch and `Closes #N`
  keep the action to what `fix-issue` opens.
