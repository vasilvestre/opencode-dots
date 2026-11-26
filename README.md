# opencode-dots

Runs a dedicated opencode agent and awf on this machine without installing anything
on the host: one image holds `opencode`, `awf`, `gh` and `awf-gh`.

## Choose what runs where

`make configure` asks a few questions and writes `.env` and
`compose.override.yaml`.

| Piece | `local` | `docker` |
|---|---|---|
| `opencode` | attaches to the `opencode serve` already running on your host | starts `opencode serve` in a container |
| `awf` | uses an `awf serve` that you start on the host | starts `awf serve` in a container |
| `gh` | mounts the host `gh` binary, no version drift | uses the `gh` baked into the image |
| `awf-gh` | runs `bin/awf-gh` on the host (needs `make build`) | runs the one-shot container (default) |
| `claude` (optional, `off` by default) | mounts the host `claude` CLI and `~/.claude`, no version drift | uses the `claude` CLI baked into the image |

`claude` is a CLI `awf` shells out to per step (`provider: claude` in a
workflow), not a server: there is no `claude serve` to attach to, so `local`
just means "use the host binary and credentials" the same way `gh` does.
Opting in does not replace opencode; workflows that should use Claude Code
instead of opencode set `provider: claude` themselves.

Docker mode is experimental and not encouraged. `local` is the supported path:
the image builds and the binaries answer, but nothing runs it on a schedule,
tower memory is not available in a container, and the provider credentials path
is untested. Start with `local` and stay there unless you are changing the
image.

### Run everything natively

With `opencode`, `awf`, `gh` and `awf-gh` all `local`, the image is never used:
skip `make image`, `make up` and any rebuild timer. `awf-gh=local`
forces `gh=local`.

```bash
make build                                   # bin/awf-gh from $AWF_GH_SRC
bin/configure --awf=local --awf-gh=local     # regenerates awf-gh.yaml from repos.yaml with host paths
systemctl --user edit dots-awf-gh.timer      # [Timer] Unit=dots-awf-gh-local.service
```

`systemd/dots-awf-gh-local.service` replaces `dots-awf-gh.service`; the timer
stays. It names `~/projects/perso/opencode-dots`: for a checkout elsewhere,
replace that path in the copied unit with `sed -i "s|%h/projects/perso/opencode-dots|$PWD|g"`. `make awf-gh` runs the native binary when `.env` has `DOT_AWFGH=local`.

What `awf-gh` needs at runtime, read from its source: `gh` on `PATH` (`gh pr list`,
`gh api`, `gh issue list`) with `GH_TOKEN` or a logged-in keyring; HTTP to
`awf_url`; write access to `state_path` and its `.exec` sidecar (the directory is
created). No `git`, no `jq`. Flags: `-config`, `-dry-run`, `-seed`, `-forget`.

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

The dashboard sends a separate notice when any recorded run finishes with
`workflow.completed` or `workflow.failed`. It posts the workflow name and that
state once per execution. Open the Config view to set the webhook and enable
these notices. The page saves the setting in `notifications.json` at the
repository root, which is private and gitignored. Start from
`notifications.example.json`:

```bash
cp notifications.example.json notifications.json
```

Set `webhook` to a Discord channel URL of the form
`https://discord.com/api/webhooks/<id>/<token>` and set `enabled` to `true`,
either in the file or on the Settings page. A save takes effect on the next
finished run without a restart. A dead webhook or a slow network only writes a
line to the dashboard log. This setting is separate from `DISCORD_WEBHOOK`: the
workflow `notify` steps keep using the environment variable. There is no test
button; saving the config does not send a message.

## Tend: learn from what shipped

`reply-pr` and `review-pr` react. `tend-repo` looks back. Once a day,
`bin/tend` runs it for every repository with `tend` enabled in `repos.yaml`
(it reads the generated `tend.list`):

```bash
$EDITOR repos.yaml       # tend: true, or tend: {auto_merge: true}
make repos
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

## The list of repositories

One file says which repositories the dot works on and what it does on each:
`repos.yaml`. The names are private and this repository is public, so it is
gitignored and only `repos.example.yaml` is versioned:

```bash
go build -o bin/repos ./cmd/repos
bin/repos import        # existing install: builds repos.yaml from awf-gh.yaml, tend.list and repos.map
cp repos.example.yaml repos.yaml   # new install only, never over an existing awf-gh.yaml
$EDITOR repos.yaml      # one block per repository
make repos              # writes awf-gh.yaml, tend.list, repos.map and sync.map
make configure          # fits awf_url and state_path to local or docker mode
```

A repository block holds feature keys: `review`, `reply`, `fix_issue`, `fix_ci`
(the watcher rules), `tend`, `checkout`, `sync` and `provider`. `defaults`
supply values to every repository that enables a feature (they never switch one
on), and a `profiles` entry bundles keys a repository takes with `use`.
`bin/repos features` lists the keys with what each does.

`awf-gh.yaml`, `tend.list`, `repos.map` and `sync.map` are generated: do not edit
them. The dashboard validates `repos.yaml` when it saves it, refuses a file that
would not generate, and rewrites the four files. `make configure` regenerates
`awf-gh.yaml` with the `awf_url` and `state_path` that fit local or docker mode.

Coming from the four hand-written files, `bin/repos import` builds the first
`repos.yaml` from them, and its test shows regenerating gives the same rules.
See [ADR 0022](ADR/0022-repos-yaml-is-the-source-of-truth.md).

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
- A repository's `checkout:` in `repos.yaml` (generated into `repos.map`) makes
  a run reuse an existing checkout instead of cloning:

```yaml
repos:
  owner/repo:
    checkout: /absolute/path
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

With `awf-gh=local`, enable the same timer after the drop-in above; do not enable `dots-awf-gh-local.service` itself. `dots.service` runs `docker compose up -d` and stays active. `dots-awf.service`
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
http://127.0.0.1:8099. From another machine on the tailnet, the same page
answers on the tailnet address that `tailscale serve` publishes. Copy
`systemd/dots-dashboard.env.example` to `systemd/dots-dashboard.env` and set
`DOTS_ALLOW_HOST` to that name. The process still binds loopback and nothing
listens on a public interface. The dashboard has no login: every device on the
tailnet can then edit workflows, change model defaults and start runs, so only
trusted devices may join the tailnet (see
[ADR 0013](ADR/0013-dashboard-on-the-tailnet.md)).
It shows:

- awf executions running now, with their current step and a live log that
  includes what the agent said on its turn
- the run history, its success, failure and average duration counters, and the
  price of each run that spent an agent turn
- Replay on a finished run: it reopens the run dialog of the same workflow with
  exact inputs when the browser kept them, otherwise recovered inputs you check
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
- a Workflows tab: a workflow drawn as a diagram, with a detail panel per step,
  plus an editor linked to the diagram that
  saves, validates and runs the file without leaving the page
- a Settings tab: the local configs in the same editor, plus Model defaults for
  global presets and per-role overrides, the Discord webhook, the desktop
  notification switch and the theme
- a Cost tab: every pull request, issue and repository the runs touched, the
  total spent on it and which workflow spent what. The grouping comes from the
  run logs, so it covers the runs the dashboard recorded
- a Memory tab: per repository, the rules in `state/tend/<owner>/<repo>/lessons.md`
  and the facts in `memory.md`, with the date the last pass covered
- a Repos tab: one row per repository in `repos.yaml`, with a checkbox for
  review, reply, fix issue, fix CI, tend and sync and a select for the agent CLI.
  A click edits `repos.yaml` at once (comments are kept, the previous file is
  stamped as a backup) and the watcher files are regenerated; a change the
  generator refuses is not written. The row's details show the watcher rules, the
  PRs already seen and a warning when Tower indexes a repository whose
  `.towerignore` is missing or lists no pattern. Custom rules (a block or a list)
  show a gear and stay editable in the YAML editor.

The page proxies `/awf/*` to `http://127.0.0.1:2511`, so it needs `awf serve`
running (`make up`). It binds loopback only. It writes `state/logs/` (awf
forgets an execution's events once the run leaves its session registry, so the
dashboard records them while it runs and deletes files older than 30 days),
`state/asks/` (the Ask conversation), and the files you edit from the page.

The generic editor saves only `workflows/*.yaml`, `awf-gh.yaml`, `tend.list`,
`repos.map` and `notifications.json`; the previous content stays as a stamped
`.bak` beside the file. The Model defaults form also writes the tracked root
`model-presets.yaml`. The page never commits: read the diff and commit yourself.
See [ADR 0004](ADR/0004-dashboard-edits-local-config.md),
[ADR 0018](ADR/0018-dashboard-managed-model-presets.md) and
[ADR 0017](ADR/0017-dashboard-edits-completion-notifications.md).

The Config view manages global presets in `model-presets.yaml` (version 1).
`light` covers `ask-pr/model`, `fix-ci/model`, `reply-pr/model` and
`review-pr/address_model`. `default` covers `_fix-issue-owned/model`, `_fix-issue-external/model`, `tend-repo/model`
and `review-pr/review_model`. A role can set an optional custom override; an
override wins over its preset.

A repository routed to `provider: claude` cannot use those IDs: the `claude`
CLI takes `sonnet`, `haiku` or `claude-sonnet-4-5`, not `provider/model`. So
the file holds a second pair, `claude_default` (`review-pr/claude_review_model`)
and `claude_light` (`review-pr/claude_address_model`), with their own syntax.
`review-pr` picks the pair that matches its `provider` input. They default to
`sonnet` and `haiku`, and a file without them stays valid. See
[ADR 0019](ADR/0019-claude-model-presets.md).

The dashboard eagerly projects those resolved IDs into workflow input
defaults. awf still reads concrete workflow defaults. On first use, migration
derives `light` from `ask-pr/model` and `default` from `_fix-issue-owned/model`. Every
other current value becomes an override, so no runtime model changes. The first
save creates `model-presets.yaml`.

Saved changes apply to the next run without restarting awf or OpenCode.
Automatic dispatch keeps using saved workflow defaults. The Run dialog still
overrides one run, and a running request keeps its started model. A
`tend-repo` override does not change the resolved roles of its nested
`review-pr` run. See [ADR 0018](ADR/0018-dashboard-managed-model-presets.md).

Prices come from awf itself: since v0.12.0 it records what each agent step cost
(`cost_usd`) in the run log, and the page sums that per run. A run started by an
older awf has no price and shows none.

Ask starts `workflows/ask-pr.yaml` with the message as input. The conversation
is a file, so every turn remembers the previous ones, and the agent reads what
is already on disk (`state/replies/pr<N>/`) before the fresh GitHub data. It
may write only its answer: the workflow makes no clone and the prompt forbids
commits, pushes and comments. Nothing feeds back into a verdict by itself. See
[ADR 0007](ADR/0007-ask-the-agent-about-a-pull-request.md).

Replay has two sources. A dashboard-started run keeps its exact inputs in the
browser that started it. For any other run, the page reads them back from the
run log, where every prompt spells `owner/repo#123`; it labels that as a guess
so you check the fields before running. Replaying an automatic run may omit
model inputs and use the current saved defaults.

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
