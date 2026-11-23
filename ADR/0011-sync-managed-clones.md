# 0011. Sync managed clones when main moves

- Status: accepted
- Date: 2026-11-23
- Supersedes: none
- Superseded by: none

## Context

The watch workflows open pull requests and merge them into `main`. The local
clones do not follow: after a merge, a checkout stays on the old commit until
you pull by hand. The clone is what the agent reuses to branch work from, so a
stale clone means new work starts behind.

`awf-gh` cannot carry this. It only knows open pull requests and issues
(`pr-opened`, `pr-updated`, `checks-failed`, `review-comment`, `issue-opened`,
`issue-labeled`). A merged pull request leaves its poll, and there is no
`pr-merged` or `push` event. Adding one means a change to `awf-gh` and an image
rebuild for a job git already does.

A working tree cannot be updated blindly. Measured on 13 managed checkouts, 7
had uncommitted files, one of them 99. A clone is also shared: the dream corpus
`cd`s into these directories, and a session can open a worktree there, so two
or three processes may read or write a checkout at once.

## Decision

We add `bin/sync`, a `dots-sync.timer` that runs it every five minutes, and a
`sync.map` of `owner/repo=/path` lines. For each path, `bin/sync` fetches
`origin/main` and fast-forwards the checkout only when the tree is clean, the
branch is `main`, and no `index.lock`, `MERGE_HEAD`, `rebase-merge` or
`rebase-apply` is present. It runs `git merge --ff-only`, so it never rewrites
history and a divergence is left alone. A `flock` keeps one run at a time. Any
other state is skipped and reported as `dirty`, `not on main`, `busy`,
`fetch failed` or `divergent`.

The map covers the repositories the dot manages, minus three the maintainer
dropped: `opencode-slides`, `opencode-settings-ui` and
`opencode-configuration-private`.

## Consequences

A clean checkout on `main` follows the remote every few minutes, so new work
branches from a fresh `main`. A checkout with uncommitted work or on another
branch is never touched, which covers the case where you or a session is
working there. Nothing is stashed: a dirty tree is reported, not rewritten, so
a `stash pop` conflict can never leave markers or hide work.

The timer does not know a process is about to write two seconds after the pull.
A checkout that looks clean can still be edited during the merge; `--ff-only`
aborts on conflict and leaves the tree intact, so the failure is safe rather
than silent.

## Alternatives

- **Add a `pr-merged` trigger to `awf-gh`.** Correct shape for a push event, but
  a Go change, a rebuild and a new workflow for a task `git fetch` does.
- **Stash, pull, pop on a dirty tree.** It loses on the measured case: a 99-file
  work in progress with concurrent readers can conflict on `pop` and leave the
  work stashed. Skipping keeps the work where it is.
- **Run `git pull` on every checkout under `~/projects`.** It would touch
  repositories the dot does not manage and clones that are checked out on other
  branches. The map stays explicit.
