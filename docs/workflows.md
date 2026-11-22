# Workflows

Three workflows live in `workflows/`. Each one is a small state machine that
`awf` runs to completion. You can run any of them by hand; the timers only
decide when.

| Workflow | Trigger | Spends an agent pass | Can push code |
| --- | --- | --- | --- |
| `review-pr` | `awf-gh`, on `pr-opened`, `pr-updated`, `checks-failed` | yes | no |
| `reply-pr` | `awf-gh`, on `review-comment` | yes | yes, on branches you own |
| `tend-repo` | `bin/tend`, daily | yes, one per repo | yes, through a pull request |

Every workflow reads `STATE_DIR` from the environment. With `opencode=local`
that is an absolute host path, because the local opencode server resolves paths
on the host.

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

1. `fetch` writes `meta.json` and `diff.patch` to
   `$STATE_DIR/reviews/pr<N>-<sha>/`.
2. `review` asks the agent to write `review.md`, or nothing when the pull
   request needs no comment.
3. `post` runs `gh pr review --comment --body-file`.
4. `notify` posts to Discord when `DISCORD_WEBHOOK` is set.

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
2. `reply` asks the agent to answer in `reply.md` and to make the requested
   change in the worktree.
3. `push` commits and pushes, but only when the head repository belongs to you
   and the branch is not `main` or `master`.
4. `post` comments on the pull request, with the pushed commit when there is
   one.
5. `notify` posts to Discord.

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
own: the maintainer reviews first. With `true`, the workflow asks GitHub to
merge the pull request once the checks pass.

Steps:

1. `survey` collects the pull requests updated since the cursor, the comments
   they received, the diffs of your commits, and the existing issues. It writes
   `material.md` in `$STATE_DIR/tend/<owner>/<repo>/` and adds a worktree of the
   default branch.
2. `learn` asks the agent for three things: rewrite `lessons.md`, write
   `findings.json`, and optionally fix one mechanical defect in the worktree
   with a `pr.md` describing the pull request.
3. `file` opens an issue per finding, skipping titles that already exist.
4. `patch` commits the worktree change on a `tend/<date>` branch, pushes it and
   opens the pull request. With `auto_merge=true` it then runs
   `gh pr merge --auto --squash`.
5. `notify` sends one digest and moves the cursor to today. A failed pass keeps
   the old cursor, so tomorrow replays the same window.

### Lessons feed the other workflows

`lessons.md` is keyed by repository, and `review-pr` and `reply-pr` read the
same file. A mistake the pass learned from therefore reaches the next reply in
that repository, not only the pass that found it.

## Run one by hand

```bash
cd ~/projects/perso/opencode-dots
GH_TOKEN=$(gh auth token) docker compose run --rm \
  -e GH_TOKEN="${GH_TOKEN}" -e STATE_DIR="$PWD/state" \
  awf bash -c "cd $PWD/state && awf run reply-pr \
    --input repo=owner/repo --input pr=42 --input event=review-comment"
```

Replace the workflow name and the inputs to run another one. `awf validate
<workflow>` checks the file before you spend a run.

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
      model: opencode-go/deepseek-v4.1-flash
    prompt: |
      Read {{.env.STATE_DIR}}/input.md and write the answer to
      {{.env.STATE_DIR}}/answer.md.
    timeout: 600
    on_success: done
    on_failure: error
```

Set the model explicitly. The free default hits a shared rate limit within
minutes and every agent step then fails with `provider.free-limit`.

## Next:

```bash
make validate                    # parse every workflow
cp tend.list.example tend.list   # then list the repositories to tend
bin/tend                         # one pass, by hand
systemctl --user enable --now dots-tend.timer
```
