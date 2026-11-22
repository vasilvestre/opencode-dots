# opencode-dots

Runs a dedicated opencode agent and awf on pop-os without installing anything
on the host: one image holds `opencode`, `awf`, `gh` and `awf-gh`.

## Choose what runs where

`make configure` asks three questions and writes `.env` and
`compose.override.yaml`.

| Piece | `local` | `docker` |
|---|---|---|
| `opencode` | attaches to the `opencode serve` already running on your host | starts `opencode serve` in a container |
| `awf` | uses an `awf serve` that you start on the host | starts `awf serve` in a container |
| `gh` | mounts the host `gh` binary, no version drift | uses the `gh` baked into the image |

The gh token always comes from `.env`: a container cannot read your keyring.

`opencode=local` reuses your running server, so the awf container joins the
host network and mounts `state/` at the same absolute path. The opencode server
runs tools on the host and sees host paths only. Point it at a container path
and the agent will not find `$STATE_DIR`.

## Build and prove it

```bash
make configure
make check
```

`make check` compiles `awf-gh` from `~/projects/oss/awf-gh` into `bin/`, builds
the image, and prints the version of every binary plus `awf serve --help`.
`make check` passing means the stack is real; it does not mean a workflow ran.

```bash
make up     # start what your modes selected
make logs
docker compose --profile jobs run --rm awf-gh awf-gh --config /work/awf-gh.yaml --dry-run
```

## Discord notifications

`review-pr` and `reply-pr` end with a `notify` step. When `DISCORD_WEBHOOK` is
set in `.env`, it posts one line plus the PR link to that channel webhook. The
`reply-pr` step stays silent when the agent decided no answer was needed. The
step never fails the workflow: an unset variable or a dead webhook only prints
a line in the logs.

Create the webhook in the target channel: channel settings, Integrations,
Webhooks, New Webhook, Copy Webhook URL.

## Tend: learn from what shipped

`reply-pr` and `review-pr` react. `tend-repo` looks back. Once a day,
`bin/tend` runs it for every repository in `tend.list`:

```bash
cp tend.list.example tend.list
$EDITOR tend.list        # owner/repo auto_merge
bin/tend                 # or: systemctl --user start dots-tend.service
```

One pass reads what happened since the last one (my commits with their diffs,
the comments I received, the issues already filed) and writes three things:

- `state/tend/<repo>/lessons.md`: durable rules the material proves. `reply-pr`
  and `review-pr` read this file before acting, so a mistake made once becomes
  a rule for every later run in that repository.
- Up to two issues, deduplicated by title against the existing ones.
- At most one pull request, only when the fix is mechanical and under twenty
  lines. `auto_merge=true` asks GitHub to merge it once the checks pass; leave
  it false for repositories you do not own.

The timer is not enabled by the install, because each pass spends one agent run
per repository:

```bash
systemctl --user enable --now dots-tend.timer
```

## Watch list

The repos the watcher polls are private names, and this repository is public.
`awf-gh.yaml` is therefore gitignored and only `awf-gh.example.yaml` is
versioned:

```bash
cp awf-gh.example.yaml awf-gh.yaml
$EDITOR awf-gh.yaml     # add your private repos
```

## What the workflows do

- `review-pr` reviews a pull request I open.
- `reply-pr` answers a review comment and pushes the requested change.
- `tend-repo` reads what happened in a repository, keeps its lessons, files the
  defects as issues and opens at most one pull request, with optional
  auto-merge.

`docs/workflows.md` documents each one: its trigger, its inputs, its steps, and
a minimal skeleton to write your own.

## Where the clones live

The review and fix workflows never touch your own checkouts. They fetch the
pull request branch into a clone and add a detached worktree for the run:

- `WORK_ROOT` (default `$STATE_DIR/work`) holds the clones, one per repository,
  reused across pull requests. Set it in `.env` unless you also mount it: a
  local opencode server only reads host paths.
- Each run works in its own worktree under `$STATE_DIR/replies/pr<N>/work`, so
  two pull requests on the same repository never share a working tree.
- `repos.map` makes a run reuse an existing checkout instead of cloning. One
  `owner/repo=/absolute/path` per line:

```bash
cp repos.map.example repos.map   # gitignored
$EDITOR repos.map
```

The workflow only fetches objects into that checkout and adds a worktree. Your
branch, your index and your working tree stay as they are: no `git pull`, no
checkout, no stash.

Replaying one event. The watcher records a key per dispatched event, so it
never answers the same comment twice. To replay one on purpose, drop its key
and let the next run see it again:

```bash
docker compose --profile jobs run --rm awf-gh \
  awf-gh --config /work/awf-gh.yaml --forget 7086166143
systemctl --user start dots-awf-gh.service
```

`--forget` matches any part of a key, so a comment id or `owner/repo#42` both
work. Forget a comment id, not a whole pull request: a repository met for the
first time only fires `pr-opened`.

## The agent is in this repo

`agent/` is mounted as the container's opencode config root, so the bot's
instructions, agents and skills are versioned here and never mixed with your
personal `~/.config/opencode`.

- `agent/AGENTS.md`: the standing rules.
- `agent/agents/assistant.md`: the agent, its model and its permissions.

This mount applies to `opencode=docker`. With `opencode=local`, the agent uses
your own `~/.config/opencode` and its default agent instead.

## Systemd

```bash
mkdir -p ~/.config/systemd/user
cp systemd/* ~/.config/systemd/user/
systemctl --user daemon-reload
systemctl --user enable --now dots.service
systemctl --user enable --now dots-awf-gh.timer
```

`dots.service` runs `docker compose up -d` and stays active. The timer runs one
`awf-gh` pass every two minutes. The dedup state lives in `./state`, bind-mounted
at `/state` and gitignored: a `compose down` keeps it, and removing that
directory resets it.

## Security

The agent has a shell inside a container that mounts `~/projects` and your gh
auth. Treat the opencode port as a root shell on your files: keep it on
loopback, and do not expose it through a tunnel without authentication.

The PR workflow reads untrusted text. The agent step writes a review; a shell
step posts it with `gh pr comment`. The agent never runs the PR's tests.

## Tower

Tower indexes a workspace in a daemon and exposes it to agents over MCP. It
overlaps what opencode already does (`read`, `glob`, `grep`, `edit`), so the
gain shows up only on large or multi-repo workspaces. It is `v0.1.0`.

Test it before baking it in:

```bash
make check                                 # image without tower
docker run --rm -it -v "$HOME/projects:/workspace" opencode-dots:latest bash
# inside: curl the tower tarball, run it on one small repo, see what it buys
```

Then build with `--build-arg WITH_TOWER=1` and declare it in
`agent/opencode.jsonc` as an MCP server. The Dockerfile already knows where the
tarball is.

## Dashboard

`make dashboard` builds `cmd/dashboard` and serves a page on
http://127.0.0.1:8099. It shows:

- awf executions running now, with their current step
- the run history and its success, failure and average duration counters
- reviewed pull requests from `state/reviews/`, with the review and the diff
  behind a click
- answered pull requests from `state/replies/`, with the reply and the pushed
  commit
- the pull requests the watcher already saw

The page proxies `/awf/*` to `http://127.0.0.1:2511`, so it needs `awf serve`
running (`make up`). It binds loopback only and reads `state/` without writing.

Flags: `-addr`, `-state`, `-awf`, `-ui`.

```bash
./bin/dashboard -state ~/elsewhere/state -awf http://127.0.0.1:2511
```

## Next:

```bash
cd ~/projects/perso/opencode-dots
make check
```
