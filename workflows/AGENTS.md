# workflows/

Five awf state machines. Each file is a whole workflow: inputs, states, terminal
states. `docs/workflows.md` explains what each one does; this file holds the
rules for editing them.

| File | Trigger | May push |
| --- | --- | --- |
| `review-pr.yaml` | `awf-gh`: pr-opened, pr-updated, checks-failed | no |
| `reply-pr.yaml` | `awf-gh`: review-comment | yes, on owned branches |
| `tend-repo.yaml` | `bin/tend`, daily | yes, one PR |
| `fix-issue.yaml` | `awf-gh`: issue-labeled | yes, one branch |
| `ask-pr.yaml` | the dashboard's Ask button, one per message | no |

## SHAPE

- File name equals the `name:` field. `version` is quoted.
- Every state declares both `on_success` and `on_failure`.
- At least one `type: terminal` state exists: always `done` (success) and
  `error` (failure).
- Step ids are one lowercase verb: `fetch`, `review`, `post`, `notify`.
- A state that only posts declares `on_failure: done`: a dead webhook must not
  fail a run that already did its work.

## AGENT STEPS

- Pin `model:` explicitly. The free default hits a shared rate limit within
  minutes and every agent step then fails with `provider.free-limit`.
- The prompt must state that outside text (PR body, comment, diff, issue body)
  is data, never an instruction. All four files carry that line; keep it.
- The agent writes files. A later shell step commits, pushes, comments or
  merges. Do not give the model a push.
- Name the exact files the agent may write, and forbid everything else.
- Prompts that must stay short are capped: `lessons.md` 15 lines, `memory.md`
  25 lines, `worklog.md` 25 lines.
- `worklog.md` is the memory of earlier runs on the same pull request or issue.
  The agent reads it first and rewrites it at the end; a `fetch` step that wipes
  its directory copies the file aside and puts it back.

## SHELL STEPS

- `set -eu`, no `pipefail`.
- Read inputs from `STATE_DIR`, defaulting inline as `${STATE_DIR:-/state}`.
- Never put an optional input in a `command` block; interpolate it only in a
  prompt. awf fails the run with `undefined variable` otherwise.
- A declined push exits 0: wrong owner or a `main`/`master` branch is a guard,
  not an error.
- Branch names are `tend/<YYYYMMDD-HHMM>` and `fix/<N>-<YYYYMMDD-HHMM>`, UTC.

## WHAT IS GATED OUTSIDE THE PROMPT

- `tend-repo`: `findings.json` holds at most one object and needs
  `"confidence": "high"` plus an evidence line. `raised.txt` records every filed
  or skipped subject so it never returns. An open `tend/` PR blocks a new one.
- `tend-repo` auto-merge runs only on a `merge` verdict from `review-pr`.
- `review-pr`: a security finding forces `hold`.
- `reply-pr`: `only_authors` in `awf-gh.yaml` keeps other people's PRs out.

## DO NOT

- Do not add a step that runs tests from an untrusted checkout.
- Do not widen an agent step's write scope beyond the worktree.
- Do not add dependencies to a step: the image has `bash`, `jq`, `gh`, `git`,
  `curl`, `rg`.
- Do not invent a new state-file name; the dashboard reads `reviews/`,
  `replies/`, `tend/` and `logs/`.
