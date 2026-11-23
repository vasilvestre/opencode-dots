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

Docker mode is experimental and not encouraged. `local` is the supported path:
the image builds and the binaries answer, but nothing runs it on a schedule,
tower memory is not available in a container, and the provider credentials path
is untested. Start with `local` and stay there unless you are changing the
image.

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
- At most one issue, and only when the material proves the defect: a red check,
  a revert, a fix commit that followed mine, a maintainer or CI comment, or a
  reported wrong output. `state/tend/<repo>/raised.txt` records every subject
  a pass filed or skipped, so it does not come back under another wording.
- At most one pull request, only for that finding, only when the fix is
  mechanical and under twenty lines, and none while an earlier `tend/` pull
  request is still open. `auto_merge=true` asks GitHub to merge it once the
  checks pass; leave it false for repositories you do not own.

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
  auto-merge. A rule it finds that holds beyond that repository goes to
  `~/dreams/dreaming/inbox/` and then through the normal dream promotion. The
  reasons for that split, with the measurements behind them, are in
  [`docs/memory.md`](docs/memory.md).

An issue carrying the `dot` label becomes one pull request, through
`fix-issue`; see `docs/workflows.md`.

`docs/workflows.md` documents each one: its trigger, its inputs, its steps, and
a minimal skeleton to write your own.

## Decisions

`ADR/` keeps one record per decision that shaped the stack: what we chose, why,
and what it costs. [`ADR/README.md`](ADR/README.md) indexes the records and
states when to write one. An accepted record is never edited; a new one
supersedes it, and both files name the other.

```bash
bin/adr new <slug>   # next free number, from ADR/template.md
make adr             # numbering, index rows and supersede links agree
```

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
systemctl --user enable --now dots-awf.service
systemctl --user enable --now dots-awf-gh.timer
systemctl --user enable --now dots-dashboard.service
```

`dots.service` runs `docker compose up -d` and stays active. `dots-awf.service`
runs the local `awf serve` when `bin/configure` picked `awf=local`; skip it for
`awf=docker`, where the container serves the API. The timer runs one
`awf-gh` pass every two minutes. `dots-dashboard.service` keeps the page on
127.0.0.1:8099 up, so it records run logs even when no browser is open. The
dedup state lives in `./state`, bind-mounted at `/state` and gitignored: a
`compose down` keeps it, and removing that directory resets it.

## Security

The agent has a shell inside a container that mounts `~/projects` and your gh
auth. Treat the opencode port as a root shell on your files: keep it on
loopback, and do not expose it through a tunnel without authentication.

The PR workflow reads untrusted text. The agent step writes a review; a shell
step posts it with `gh pr comment`. The agent never runs the PR's tests.

## Tower

Tower indexes a workspace in a daemon and exposes it to agents over MCP, plus a
durable agent-memory store. The Linux release tarballs are still empty (PR
awf-project/tower#99), so build it from source:

```bash
git clone https://github.com/awf-project/tower /tmp/tower-src
cd /tmp/tower-src
make install                 # tower -> ~/.local/bin
make install-extensions GLOBAL=1
```

The `agent_memory` extension gets storage access only when its binary sits next
to `tower`, and `make install` does not copy it yet (PR awf-project/tower#100).
Until that lands, link it:

```bash
ln -sfn ~/.local/share/tower/extensions/agent_memory/agent_memory_extension \
  ~/.local/bin/agent_memory_extension
```

Then declare `tower mcp` in `~/.config/opencode/opencode.jsonc` under
`mcp.servers`. With `DOT_OPENCODE=local` that is enough: the opencode server
runs on the host, so it finds `tower` on `PATH`. `tower init` in a repository
scaffolds `.towerignore` and `.tower/config.toml`; without `tower init` the
daemon warns and indexes every non-hidden file.

The image build keeps `WITH_TOWER=0` for `DOT_OPENCODE=docker` until the
tarballs carry a binary. Tower is a bonus, never a requirement: the workflows
write their own memory of a thread to
`state/replies/pr<N>/worklog.md` and `state/fix/<repo>/issue-<N>/worklog.md`,
and the agent reads and rewrites that file on every run. When tower memory is
reachable, the same agent also stores one `session_summary` record per run with
the pull request or issue number as provenance.

## Dashboard

`make dashboard` builds `cmd/dashboard` and serves a page on
http://127.0.0.1:8099. It shows:

- awf executions running now, with their current step and a live log that
  includes what the agent said on its turn
- the run history, its success, failure and average duration counters, and the
  price of each run that spent an agent turn
- Replay on a finished run: it reopens the run dialog of the same workflow with
  the inputs that run used, so a fix can be retested without retyping them
- reviewed pull requests from `state/reviews/`, with the review and the diff
  behind a click
- answered pull requests from `state/replies/`, with the reply and the pushed
  commit
- Ask on a reviewed or answered pull request: a conversation with the agent,
  kept in `state/asks/<owner>/<repo>/pr<N>/thread.md`, which answers one
  question per run and can read the reply and checkout an earlier run left;
  when the answer names a next workflow, the message carries a button that
  opens the run dialog for it
- the pull requests the watcher already saw
- a Workflows tab: the success path of a workflow in order, each step stating
  where it goes on success, on failure and on a branch, plus an editor that
  saves, validates and runs the file without leaving the page
- a Config tab: `awf-gh.yaml`, `tend.list` and `repos.map` in the same editor
- a Memory tab: per repository, the rules in `state/tend/<owner>/<repo>/lessons.md`
  and the facts in `memory.md`, with the date the last pass covered
- a Repos tab: every repository in `awf-gh.yaml`, the workflows that watch it,
  their triggers and author filters, and the PRs the watcher already saw. A
  warning marks the repositories Tower indexes whose `.towerignore` is missing
  or lists no pattern.

The page proxies `/awf/*` to `http://127.0.0.1:2511`, so it needs `awf serve`
running (`make up`). It binds loopback only. It writes three things:
`state/logs/` (awf forgets an execution's events once the run leaves its
session registry, so the dashboard records them while it runs and deletes files
older than 30 days), `state/asks/` (the Ask conversation), and the four local
config files you edit from the page.
Saving is limited to `workflows/*.yaml`, `awf-gh.yaml`, `tend.list` and
`repos.map`; the previous content stays as a stamped `.bak` beside the file.
The page never commits: read the diff and commit yourself. See
[ADR 0004](ADR/0004-dashboard-edits-local-config.md).

Prices come from awf itself: since v0.12.0 it records what each agent step cost
(`cost_usd`) in the run log, and the page sums that per run. A run started by an
older awf has no price and shows none.

Ask starts `workflows/ask-pr.yaml` with the message as input. The conversation
is a file, so every turn remembers the previous ones, and the agent reads what
is already on disk (`state/replies/pr<N>/`) before the fresh GitHub data. It
may write only its answer: the workflow makes no clone and the prompt forbids
commits, pushes and comments. Nothing feeds back into a verdict by itself. See
[ADR 0007](ADR/0007-ask-the-agent-about-a-pull-request.md).

Replay has two sources. A run the page started keeps its exact inputs in the
browser that started it. For any other run, the page reads them back from the
run log, where every prompt spells `owner/repo#123`; it labels that as a guess
so you check the fields before running.

Flags: `-addr`, `-state`, `-root` (repository holding the editable files),
`-awf`, `-ui`, `-awfgh` (watcher config, default `awf-gh.yaml`),
`-opencode-db` (empty finds it under `$XDG_DATA_HOME`, `~/.local/share` or
`state/`), `-opencode` (default the opencode config under `$XDG_CONFIG_HOME`),
`-transcripts` (empty finds awf's per-run transcripts in the same
way).

```bash
./bin/dashboard -state ~/elsewhere/state -awf http://127.0.0.1:2511
```

## Next:

```bash
cd ~/projects/perso/opencode-dots
make check
```
