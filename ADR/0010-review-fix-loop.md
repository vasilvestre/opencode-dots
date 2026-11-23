# 0010. review-pr closes its own loop: fix, push, re-review

- Status: accepted
- Date: 2026-11-23
- Supersedes: none
- Superseded by: none

## Context

`review-pr` writes a verdict and stops. On `hold` the pull request waits for a
person: nothing turns the review findings into a commit. `reply-pr` cannot close
the gap because it answers comments written by someone else, and the review
comment is written by the same account, so `reply-pr` treats it as my own and
skips it.

`awf-gh`'s `x-skip` list also names `vasilvestre`, so the watcher never reviews
my own pull requests on the `opencode-*` repositories. A fix-issue pull request
was therefore never reviewed, and a held one never moved.

The user wants the dot to carry its own fix-issue pull request to a merge when
no human decision is needed, and to stop with a notice when one is.

## Decision

We add three states to `review-pr`, after `post`, and let the existing events
close the loop:

- `decide`: reads the verdict. `merge` goes to `automerge`. A `hold` on my own
  branch (`fix/*`, owned head repository, `Closes #N` in the body) goes to
  `address`, unless `reviews/pr<pr>/attempts` reached
  `MAX_FIX_REVIEW_ATTEMPTS` (default 3), which writes `stuck.md` and goes to
  `notify`. Any other `hold` goes to `notify` untouched.
- `address`: an agent clears the findings in the checked-out worktree and writes
  `fixmsg.md` and `worklog.md`. It never commits or pushes.
- `push`: commits and pushes the fix on the owned branch, increments the
  per-pull-request attempt counter, and stops.

The push fires `pr-updated`, which `awf-gh` already sends to `review-pr`, so the
next review runs on the new commit. The loop ends in `automerge` on a `merge`
verdict or in `stuck.md` and a Discord notice when the budget runs out or a
finding needs a human. To let my own pull requests be reviewed, the
`opencode-dream-memory` rule in `awf-gh.yaml` drops `vasilvestre` from its
ignore list and keeps only the bots.

## Consequences

A held fix-issue pull request now reaches `main` without a person, as long as
each round makes progress. The review is re-posted every round, so the thread
carries the audit trail. Cost is bounded: at most `MAX_FIX_REVIEW_ATTEMPTS` agent
passes per pull request, each keyed by a new commit.

The loop trusts the review to decide when it is done. A finding that needs a
design or product decision is the agent's cue to write `stuck.md`, not to guess;
when it guesses wrong the next review holds again until the budget stops it, and
a person reads the notice. Only `fix/*` branches that close an issue are touched,
so a human pull request is reviewed but never edited or merged by this path.

My own pull requests in this repository now get a review comment they did not
get before. That is the point here, scoped to one repository; the other
`opencode-*` rules still skip `vasilvestre`.

## Alternatives

- **Let `reply-pr` fix the review.** It skips the comment because the author is
  the token's account; making it answer itself would reopen a self-reply loop on
  every comment.
- **Dispatch `fix-review` from `review-pr` through the awf API.** A separate
  workflow avoids the extra states, but it adds a nested run and a second place
  that reads the review. The event loop already exists, so we reuse it.
- **Turn on repository auto-merge.** Ruled out earlier (ADR 0009) and it does
  not fix the review findings either.
