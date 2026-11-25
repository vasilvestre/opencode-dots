# Workflows

Six workflows live in `workflows/`. Each one is a small state machine that
`awf` runs to completion. You can run any of them by hand; the timers only
decide when.

| Workflow | Trigger | Spends an agent pass | Can push code |
| --- | --- | --- | --- |
| `review-pr` | `awf-gh`, on `pr-opened`, `pr-updated`, `checks-failed` | yes | no |
| `reply-pr` | `awf-gh`, on `review-comment` | yes | yes, on branches you own |
| `tend-repo` | `bin/tend`, daily | yes, one per repo | yes, through a pull request |
| `fix-issue` | `awf-gh`, on `issue-labeled` | yes, one per issue | yes, one branch per issue |
| `fix-ci` | `awf-gh`, on `checks-failed` | yes, one per red commit | yes, on your own pull request branch |
| `ask-pr` | the Ask button on a pull request card | yes, one per message | no |

Every workflow reads `STATE_DIR` from the environment. With `opencode=local`
that is an absolute host path, because the local opencode server resolves paths
on the host.

## Model inputs

Every agent step reads its model from a validated workflow input. A model
identifier is `provider/model`, with an optional `#variant`. The
`provider: opencode` field selects the awf adapter, not the model provider.

Single-agent workflows (`reply-pr`, `tend-repo`, `fix-issue`, `fix-ci` and
`ask-pr`) use `model`. `review-pr` uses `review_model` for the reviewer and
`address_model` for the fixer. Keep their saved defaults different so the
reviewer does not approve its own work.

The Config view's Model defaults form edits these input defaults through
`GET /api/models` and `PUT /api/models`. Automatic runs from `awf-gh`, Ask and
`bin/tend` omit model inputs and use the saved defaults. The Run dialog can
override a model for one run.

A saved change applies to the next run without restarting awf or OpenCode. A
running request keeps the model it started with.

Only a dashboard-started run keeps its exact original inputs in browser
`localStorage`, including a model override. Replay uses that record when
available. An automatic run has no browser record, so replay may use the current
saved default instead of the historical model.

Validation checks identifier syntax, not provider availability. An unavailable
model fails the next agent step.

A `tend-repo` model override changes only its own agent step. It does not replace
the `review_model` or `address_model` used by the nested `review-pr` run.

## review-pr

Reviews a pull request and posts one review.

```yaml
# awf-gh.yaml
repos:
  - repo: vasilvestre/opencode-dots
    scope: local
    workflow: review-pr
    on: [pr-opened, pr-updated, checks-failed]
```

Steps:

1. `guard` drops the run when the head no longer matches the input sha.
   `fetch` writes `meta.json` and `diff.patch` to
   `$STATE_DIR/reviews/pr<N>-<sha>/`, then checks out the pull request head in
   `work/` beside them. The reviewer reads the callers, the types and the tests
   with `read`, `glob` and `grep`, so a finding can rest on a file outside the
   diff. When a check on that commit has failed, `fetch` also writes
   `checks.json` and `ci-failed.log`, the last 300 lines of `--log-failed` for
   up to three failed runs. Logs are best effort: a check that is not a GitHub
   Actions run leaves no log, and the review goes on without it.
2. `wait` holds the run while a check is still pending, for up to
   `CI_WAIT_SECONDS` (default 600). A settled red check is reviewed anyway: the
   log explains it. A repository with no checks passes at once.
3. `rework` alternates `review` and `address`, at most three rounds. `review`
   asks the agent to write `review.md`: a correctness section, then a security
   section that walks trust boundaries, secrets, privileges, data destruction,
   dependencies and failure paths. The agent writes `verdict.txt` with `merge`
   or `hold`; a security finding always means `hold`. `review` uses
   `review_model` and `address` uses `address_model`. Their saved defaults use
   different models, and each round starts a fresh session, so the reviewer
   cannot approve its own work.
   The reviewer lists each finding the fix round cleared as a `- cleared:` bullet
   quoting one exact line of the worktree. `verify` checks every quote with
   `grep -F`; a quote not found becomes `- unverified:`, an open finding.
   `gate` ends the loop on a review with no open finding, or on the same open
   findings as the previous round, which writes `stuck.md`. `address` clears the
   findings in the worktree, rewrites `worklog.md` with `file:line` and the new
   line for each one, and `rediff` refreshes
   `diff.patch` so the next round reads the code as the fix left it.
4. `post` runs `gh pr comment --body-file`, once per run.
   When a fix round left edits on a branch the dot may not push to (not its own
   `fix/*` branch), `post` keeps them as `fix.patch` and appends them to the
   comment under "Proposed fix, not pushed", inside a `<details>` diff block cut
   at 60000 bytes. `fetch` writes `owned` once (`ok` or the reason); `decide`
   and `post` both read it. `notify` then names `fix.patch`.
5. `decide` sends a `merge` verdict to `automerge`, a `hold` on a pull request
   `owned` marks `ok` (my own `fix/*` or a delegated one) to `push`, and anything else to `notify`. `push` commits the
   worktree and pushes it, so the next `pr-updated` starts another run, capped
   by `MAX_FIX_REVIEW_ATTEMPTS` counted in `reviews/pr<N>/attempts`.
   `automerge` merges once the verdict is `merge` and the checks are green or
   absent.
   Delegation: a line that is exactly `/dots take` in a comment or the body, written
   by me (the gh user running the dot), lets the dot fix and push my own pull
   request on any branch except `main`/`master`; `/dots stop` revokes. A comment
   counts only when the command is its whole text, because the dot posts with my
   token. The last command wins, except that a `/dots stop` in the body always
   wins (gh gives no edit time for the body, so a body `/dots take` is dated at
   creation; to re-take after a stop, delete the `/dots stop` line from the body
   first, then comment `/dots take`). Never on a fork or a `tend/`
   branch. `/dots stop` does not block my own
   `fix/*` pull request that closes an issue. `fetch` reads it and sets `owned`;
   others' commands are ignored.
   Merge is not delegated: `automerge` still needs a `fix/*` branch with
   `Closes #N`. A comment starts no run; the next event or a manual run reads it.
   Commits use the identity of `gh api user`. See ADR 0015.
6. `notify` posts to Discord when `DISCORD_WEBHOOK` is set.

## reply-pr

Answers a review comment on your pull request, and changes the code when the
comment asks for it.

```yaml
# awf-gh.yaml
repos:
  - repo: AlteredParadox/pmss-fleet-manager
    scope: local
    workflow: reply-pr
    on: [review-comment]
    only_authors: ["vasilvestre"]
```

Steps:

1. `fetch` reads the pull request, its diff and the branch name, then adds a
   detached worktree of the head branch under `$STATE_DIR/replies/pr<N>/work`.
2. `guard` settles in shell what shell can settle, so the agent never spends a
   pass on nothing. It writes `silent` and stops the workflow at `done` when the
   pull request is merged or closed, and when the last comment that is not yours
   only summons another reviewer, such as `@codex review`: that bot answers, and
   reading the whole thread to find nothing once cost 1m47. On an open one it
   also counts the rounds already answered (one comment of yours that pushed a
   commit) and writes `stuck.md` only past a high safety cap,
   `MAX_REPLY_ROUNDS` (default 10).
3. `reply` asks the agent to answer in `reply.md` and to make the requested
   change in the worktree. The agent writes `stuck.md` itself when it is blocked
   by something it cannot do (a real environment test such as the Home Assistant
   ingress, credentials or external access it lacks, a decision for a human) or
   when the thread is a real ping-pong that never converges. A review still
   making concrete progress is not a ping-pong and gets answered.

   Each run on a pull request reads `worklog.md` from the previous run, and
   rewrites it at the end: what the thread asked, what was decided or changed,
   the pushed commit, what is still open. The agent also stores one
   `session_summary` record in tower memory, so the next run finds the earlier
   work by searching `owner/repo#N`. `fetch` recreates the directory, so it
   copies `worklog.md` aside and puts it back.
4. `push` commits and pushes, but only when the head repository belongs to you
   and the branch is not `main` or `master`.
5. `post` comments on the pull request, with the pushed commit when there is
   one.
6. `notify` posts to Discord.

When `stuck.md` exists, `push` and `post` do nothing and `notify` posts the
reason with a link to the pull request, then the run stops: a human takes over.
When `silent` exists (merged or closed pull request), every step stops without a
comment and without a Discord message.

The agent never commits and never pushes; the shell steps do that, so the
identity and the branch guard stay outside the model.

## tend-repo

A daily pass over a repository: read what happened since the last pass, keep
the lessons, file the defects, open at most one pull request.

```bash
# tend.list, gitignored
AlteredParadox/pmss-fleet-manager false
vasilvestre/opencode-dots true
```

The second column is `auto_merge`. Keep it `false` for repositories you do not
own: the maintainer reviews first. With `true`, the workflow first runs
`review-pr` on the pull request it just opened and reads the one-word verdict
in `verdict.txt`. Only `merge` enables GitHub auto-merge, so a `hold` review
leaves the pull request waiting. The checks still have to pass.

`bin/tend` skips a repository with no commit and no issue or comment movement
since its cursor in `state/tend/<owner>/<repo>/since`, using two `gh api` calls
instead of an agent pass.

Steps:

1. `survey` collects the pull requests updated since the cursor, the comments
   they received, the diffs of your commits, and the existing issues. It writes
   `material.md` in `$STATE_DIR/tend/<owner>/<repo>/` and adds a worktree of the
   default branch.
2. `learn` asks the agent for five things: rewrite `lessons.md` (rules learned
   from mistakes), rewrite `memory.md` (durable facts about the repository that
   are not rules), write `findings.json`, optionally fix one mechanical defect
   in the worktree with a `pr.md` describing the pull request, and write
   `promote.md` with the rules that hold beyond this repository.
3. `file` opens one issue, and only for the finding the agent marked
   `"confidence": "high"`. Every finding it files or skips goes into
   `raised.txt`, so a later pass treats that subject as decided.
4. `patch` commits the worktree change on a `tend/<date>` branch, pushes it and
   opens the pull request. It discards the change when no high-confidence
   finding backs it, or when a `tend/` pull request is still open. With
   `auto_merge=true` it runs `review-pr` on that pull request, then
   `gh pr merge --auto --squash` only on a `merge` verdict.
5. `notify` sends one digest and moves the cursor to today. A failed pass keeps
   the old cursor, so tomorrow replays the same window.

### Dry run

`bin/tend --dry-run` runs the whole pass and opens nothing: no issue, no push,
no Discord message, and the cursor stays where it is, so the next real pass
replays the same window. The pass records what it would have opened:

- `would-issue.md` holds the issue title and body.
- `would-pr.md` holds the pull request description.
- `would-pr.diff` holds the change `patch` would have committed. The worktree
  is reset, so the next real pass does not commit it by mistake.

The dashboard shows `would-issue.md` and `would-pr.md` in the Memory view. To
start the same pass from the dashboard, open the Run dialog on `tend-repo` and
set `dry_run` to `true`.

### What is worth opening

The pass runs again tomorrow on almost the same code, so an item it opens sits
in front of a maintainer for good. Open nothing unless the material proves the
defect: a red check, a revert, a fix commit that followed mine, a maintainer or
CI comment naming the fault, a reported wrong output, or two `path:line` that
cannot both hold. Noticing something while reading is not proof, and a style
opinion is not a defect.

The gate is mechanical, not only a prompt line:

- `findings.json` holds at most one object, with `"confidence": "high"` and an
  `"evidence"` line; `file` creates nothing for an object that lacks either.
- `file` appends every finding to `raised.txt` (`filed` or `skipped`), and
  `learn` reads that file first, so the same subject does not come back under
  another wording.
- `survey` writes `open_pr`, the number of open `tend/` pull requests. When it
  is not zero, `learn` leaves the worktree alone and `patch` discards any
  change, so pull requests do not pile up.
- `patch` discards a change that no high-confidence finding backs.

An empty pass is the normal outcome.

### Lessons and memory feed the other workflows

`lessons.md` and `memory.md` are keyed by repository, and `review-pr` and
`reply-pr` read both. A mistake the pass learned from reaches the next review in
that repository, not only the pass that found it. Keep the split strict: a rule
you must follow belongs in `lessons.md`, a fact about the repository belongs in
`memory.md`. `~/dreams` covers what holds across every project, so neither file
repeats it.

### How a lesson leaves the repository

`promote.md` holds zero to three lines a pass judged true outside its
repository, written without naming it. `bin/tend` copies each new line into
`~/dreams/dreaming/inbox/<owner>_<repo>.md` in the candidate format the dream
pass writes, once: a line already in the inbox is not added again. The nightly
light pass reads that directory, so the line reaches `~/dreams/MEMORY.md`
through the same promotion the rest of your memory goes through. No repository
path is shared between the container and `~/dreams`, which is why the delivery
happens on the host, in `bin/tend`, after the pass succeeds.

Write nothing there by habit: the file is a queue, not a dump. A pass that
promotes everything makes `~/dreams` useless. [`memory.md`](memory.md) records
what the research measures about shared and unfiltered memory.

## fix-issue

Turns one labeled issue into one pull request. The label is the trigger: an
issue nobody labels is never touched, and `awf-gh` fires once per issue and
label, so a fixed issue does not come back on the next pass.

```yaml
# awf-gh.yaml
repos:
  - repo: vasilvestre/opencode-dots
    scope: local
    workflow: fix-issue
    on: [issue-labeled]
    labels: ["dot"]
```

The label must exist on the repository before you use it:

```bash
gh label create dot -R owner/repo \
  --description "Let the agent fix this issue and open a pull request" \
  --color 6c5ce7
gh issue edit 2 -R owner/repo --add-label dot
```

Steps:

1. `fetch` writes `issue.json` (title, body, author, labels, comments) to
   `$STATE_DIR/fix/<owner>/<repo>/issue-<N>/`, adds a worktree of the default
   branch, and copies `lessons.md` and `memory.md` from the last tend pass if
   they exist.
2. `fix` asks the agent to prove the defect first, quoting `path:line`, then to
   make the smallest change that fixes it and write `fix.md`: the pull request
   title, then the body. An issue that is already fixed, or wrong, ends the run
   with no pull request and no failure. A run on an issue that was already
   worked reads `worklog.md` and the tower memory record of the earlier run
   before it starts, and rewrites `worklog.md` at the end.
3. `push` commits on a `fix/<N>-<date>` branch, pushes it and opens a pull
   request whose body ends with `Closes #N`, so merging closes the issue.
4. `notify` posts the pull request URL on Discord when `DISCORD_WEBHOOK` is set.

`fix-issue` never merges. The pull request waits for your review, or for
whatever closes it.

To replay an issue, drop its key from the state file; the label stays:

```bash
docker compose --profile jobs run --rm awf-gh \
  --forget 'owner/repo#issue2'
```

## fix-ci

Turns a red check on one of your own pull requests into one pushed commit.
`awf-gh` already derives `checks-failed` from the check rollup, and its dedup
key is the head commit: an unchanged commit fires once, a new push fires again.

```yaml
# awf-gh.yaml
repos:
  - repo: awf-project/cli
    scope: local
    workflow: fix-ci
    on: [checks-failed]
    only_authors: ["vasilvestre"]
```

One rule when you add this: the dedup key has no workflow in it. A repository
cannot watch `checks-failed` with two workflows, because the first one to fire
records the key and the second never sees it. Pick `fix-ci` or `review-pr` for
that event and drop the other.

Steps:

1. `fetch` writes `meta.json`, `diff.patch`, `checks.json`, `failed-runs.txt`
   and `ci-failed.log` (the last 400 lines of `--log-failed`, three runs at
   most) to `$STATE_DIR/ci-fix/<owner>/<repo>/pr<N>/<sha>/`, and adds a
   detached worktree of the head branch in `work/`. The run directory is keyed
   by commit, so two runs on one pull request never share a worktree.
2. `guard` stops before the agent when the pull request is not open, when the
   author or the branch owner is not you, when the branch is `main` or
   `master`, when no failure log is readable, or when the fix budget is spent.
   `stuck.md` records the budget stop.
3. `fix` asks the agent to name the failure from the log, quote the line that
   proves it, and make the smallest change that fixes it. An infrastructure
   failure (timeout, cancelled run, rate limit, runner out of memory, network)
   means `stuck.md` and no change. The agent may run the cheap check the
   repository documents, and it may not weaken or skip the failing check.
4. `push` commits on the pull request branch with the first line of `fix.md` as
   the message, and pushes only when your account owns the head repository. It
   discards a worktree change that no `fix.md` backs.
5. `notify` posts to Discord when a commit was pushed or the run stopped, then
   drops the worktree.

The loop is the real risk: a fix that pushes and leaves the check red fires
again on the new commit. `push` counts its own commits in
`$STATE_DIR/ci-fix/<owner>/<repo>/pr<N>/attempts`, and `guard` stops past
`MAX_CI_FIX_ATTEMPTS` (default 2). The count expires after a day without a
push. To replay a pull request, delete that file:

```bash
rm -f state/ci-fix/owner/repo/pr123/attempts
docker compose --profile jobs run --rm awf-gh --forget 'owner/repo#123'
```

## ask-pr

Answers one written question about one pull request. The dashboard's Ask button
starts it; you do not run it by hand.

```bash
curl -X POST http://127.0.0.1:2511/api/workflows/global/ask-pr/run \
  -H 'Content-Type: application/json' \
  -d '{"inputs":{"repo":"owner/repo","pr":"42","question":"Do we merge after the checks?"}}'
```

Steps:

1. `fetch` writes `pr.json`, `diff.patch` and `pr.thread.json` (comments and
   reviews merged, oldest first) to
   `state/asks/<owner>/<repo>/pr<N>/`. It never wipes the folder: `thread.md`
   lives there.
2. `ask` is one agent step. It reads `thread.md` first, then the files above,
   then `state/replies/pr<N>/reply.md` and the read-only checkout under
   `state/replies/pr<N>/work` when an earlier run left them. It writes
   `answer.md` and nothing else.
3. `done`.

The conversation is the file. Every turn reads the whole `thread.md`, so the
agent knows what it already said, and no awf session is involved. The prompt
forbids commits, pushes and comments, and the workflow makes no clone, so there
is nothing to push. The page appends the answer and deletes `answer.md`, which
is why a failed run leaves the thread unchanged.

The answer is a comment for you, not a verdict: `review-pr` and `reply-pr` still
decide and act on their own triggers.

## Run one by hand

```bash
cd ~/projects/perso/opencode-dots
GH_TOKEN=$(gh auth token) docker compose run --rm \
  -e GH_TOKEN="${GH_TOKEN}" -e STATE_DIR="$PWD/state" \
  awf bash -c "cd $PWD/state && awf run reply-pr \
    --input repo=owner/repo --input pr=42 --input event=review-comment"
```

Replace the workflow name and the inputs to run another one. Add
`--input model=provider/model#variant` to override the saved default for one run.
For `review-pr`, use `--input review_model=provider/model#variant` and
`--input address_model=provider/model#variant`. `awf validate <workflow>` checks
the file before you spend a run.

## Anatomy of a workflow

Every file has the same shape: `name`, `version`, optional `inputs`, then
`states` with an `initial` state.

```yaml
name: hello
version: "1.0.0"
inputs:
  - name: repo
    type: string
    required: true
  - name: model
    type: string
    required: false
    default: opencode-go/deepseek-v4.1-flash
    description: model for the agent step
    validation:
      pattern: '^[A-Za-z0-9][A-Za-z0-9._+-]*/[A-Za-z0-9][A-Za-z0-9._:+/@-]*(#[A-Za-z0-9][A-Za-z0-9._:+-]*)?$'
states:
  initial: greet
  greet:
    type: step
    command: |
      set -eu
      echo "hello {{.inputs.repo}}"
    timeout: 60
    on_success: done
    on_failure: error
  done:
    type: terminal
    status: success
  error:
    type: terminal
    status: failure
```

Two rules that save time:

- A state needs `on_success` and `on_failure`, and at least one `type: terminal`
  state must exist, or the file does not parse.
- An optional input that the file interpolates must be given a value at run
  time, or the command fails with `undefined variable`. Keep optional inputs
  out of `command` blocks.

An agent step looks like this:

```yaml
  think:
    type: agent
    provider: opencode
    options:
      model: "{{.inputs.model}}"
    prompt: |
      Read {{.env.STATE_DIR}}/input.md and write the answer to
      {{.env.STATE_DIR}}/answer.md.
    timeout: 600
    on_success: done
    on_failure: error
```

Every agent step must read a validated model input with a non-empty default.
Use `provider/model`, with an optional `#variant`. The free default hits a shared
rate limit within minutes and every agent step then fails with
`provider.free-limit`.

## Next:

```bash
make validate                    # parse every workflow
cp tend.list.example tend.list   # then list the repositories to tend
bin/tend                         # one pass, by hand
systemctl --user enable --now dots-tend.timer
```
