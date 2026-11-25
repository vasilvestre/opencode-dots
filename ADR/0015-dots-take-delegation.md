# 0015. The operator delegates a pull request with /dots take

- Status: accepted
- Date: 2026-11-25
- Supersedes: none
- Superseded by: none

Relates to [0010](0010-review-fix-loop.md). It does not supersede it: 0010 still
defines the loop; this record extends which pull requests the loop may fix.

## Context

`review-pr` fixes and pushes only on a `fix/*` branch whose body closes an issue.
That naming was a proxy for permission. A pull request I opened by hand from
another branch name got a review whose fix round was thrown away, or kept only as
"Proposed fix, not pushed", even when I wanted the dot to push it.

## Decision

We let the operator delegate a pull request explicitly. A line that is exactly
`/dots take` (whitespace trimmed, CRLF accepted) in a comment or in the pull
request body grants the dot the right to fix and push; `/dots stop` revokes it.
A comment counts only when the command is its whole text: the dot posts with the
same token, so a review that echoes the line must not delegate. The last command
wins by creation time, except that a `/dots stop` in the body always wins, since
gh gives no edit time for the body. Only the authenticated gh user running
the dot (`gh api user`, the login `decide` already uses) can delegate; the same
text from anyone else is ignored and `owned` notes "delegation ignored: not
written by me". No other text is ever read as an instruction.

`fetch` reads the pull request with `gh pr view --json body,createdAt,author,comments`
and writes `owned` once. `owned` is `ok` when the old rule holds (own `fix/*`
branch with `Closes #N`) or when the delegation is active, the pull request
author is me, the head repository is the base repository (not a fork) and the
branch is not `main`, `master` or a `tend/` branch. `decide` and `post` only read `owned`; the `Closes #N` check moved from
`decide` into `fetch`. The `MAX_FIX_REVIEW_ATTEMPTS` cap applies unchanged.

Commits are made as whoever runs the dot: `push` takes name and email from
`gh api user`, and nothing is hardcoded.

## Consequences

- Merge is not delegated. `automerge` still requires a `fix/*` branch and
  `Closes #N`, so a delegated pull request ends in a human merge.
- `/dots stop` revokes at the next run; a push already made stays. It does not
  stop my own `fix/*` pull request that closes an issue: that rule is older and
  does not depend on delegation.
- A comment does not trigger a run. The next `pr-updated`, `checks-failed` or a
  manual run reads the command.
- The command text is data checked with `jq`; it never reaches a shell.
- A fork or someone else's pull request is never delegable this way.

## Alternatives

- **Allow every non-`main` branch of mine.** Too broad: a branch I am still
  writing by hand would be pushed over.
- **Read free text such as "please fix this".** It makes untrusted wording an
  instruction. An exact command line does not.
