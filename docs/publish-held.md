# Releasing a held item

At the `propose` ceiling, `bin/publish-gate` writes a record under
`$STATE_DIR/held/` and the step does not publish (`docs/autonomy.md`, ADR 0040).
`workflows/publish-held.yaml` is the way out: it publishes exactly what was
prepared, once, when a person asks.

## How to release

Start the `publish-held` workflow with one input, `id`: the file name of the
record, for example `20261122T093000Z-comment-owner_repo_12.txt`. Post it to
`awf serve` like any other run (not `awf run`, which leaves no trace). Set
`RELEASED_BY` in the run environment to name the person; without it the log
names the account behind the token.

There is no agent step. One shell step reads the record and publishes. Releasing
is the owner's explicit approval, so the repository ceiling is not consulted;
the log line says who and when.

## The record

`bin/publish-gate` writes `action`, `ref`, `autonomy`, `class`, `artifact`,
`reason`, then the optional lines below for each of `GATE_BRANCH`, `GATE_BASE`,
`GATE_PUSH_REPO`, `GATE_TITLE`, `GATE_CLOSES` that the calling step set (a line is
omitted when unset; values are cut to one line without control characters). The
workflows `fix-issue`, `reply-pr`, `fix-ci`, `review-pr` and `tend-repo` set them
where the value is known. `publish-held` validates each; none is code:

| Field | Used by | Meaning |
| --- | --- | --- |
| `branch` | push, open-pr | the branch to push, or the head of the pull request |
| `base` | push, open-pr | the base branch; for a push, used when `branch` does not exist yet |
| `push-repo` | push | `OWNER/REPO` to push to (a fork); default the pull request head or `ref` |
| `title` | push | commit message for a plain `git diff` patch |
| `closes` | open-pr | issue number, appended to the body as `Closes #N` |

A push from `fix-issue` or `tend-repo` names no `push-repo`: the release pushes to
the repository of the `ref`, which is wrong for a fork the dot has no write access
to. Such a record needs `push-repo` added by hand.

## What it does

| Action | Artifact | Release |
| --- | --- | --- |
| `comment` | the comment text | `gh issue comment N --repo REPO --body-file ARTIFACT` (works on pull requests) |
| `push` | a patch: `git diff` or `git format-patch` | fresh clone of the branch, apply, commit as the token account, push |
| `open-pr` | first line the title, the rest the body | `gh pr create`; push the branch first |

For a push without `branch`, the `ref` must be an open pull request: its head
branch and repository are used. A branch that does not exist is created from
`base` when the record names one.

On success the record moves to `$STATE_DIR/held/released/` and one tab-separated
line (time, who, action, ref, id) is appended to `$STATE_DIR/held/released.log`.
On any failure the record stays where it is, so the release can be retried.

## What is checked

- The id is a plain file name: no path, no leading dot, no `..`.
- The record exists under `held/` and is not already in `held/released/`.
- `action` is `comment`, `push` or `open-pr`; `ref` is `OWNER/REPO#N`.
- The artifact exists, is not empty and lives under `STATE_DIR`.
- Branch, base, push-repo and closes match a strict pattern.
- One release of a record at a time (a lock directory).
- Before a push, `bin/held` (ADR 0026): a human claim on the branch refuses the
  release; a check that cannot run is logged and the release goes on, as in the
  other workflows.

## What is refused

A refusal fails the run with a one-line reason and changes nothing.

- A `merge` record: merging is never released this way.
- A missing, malformed or already released record, or an artifact outside
  `STATE_DIR`.
- A push to `main` or `master`, or a pull request from one.
- A branch under a human claim.
- A push on a pull request that is not open, or a branch that is gone with no
  `base` to recreate it from.

Text in a record or an artifact is data: it never reaches a shell as code and
never starts an agent.

## Test

`bin/test-publish-held` runs the step offline with fake `gh` and `git`.
