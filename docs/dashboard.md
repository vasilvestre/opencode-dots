# The dashboard



`make dashboard` builds `cmd/dashboard` and serves a page on
http://127.0.0.1:8099. From another machine on the tailnet, the same page
answers on the tailnet address that `tailscale serve` publishes. Copy
`systemd/dots-dashboard.env.example` to `systemd/dots-dashboard.env` and set
`DOTS_ALLOW_HOST` to that name. The process still binds loopback and nothing
listens on a public interface. The dashboard has no login: every device on the
tailnet can then edit workflows, change model defaults and start runs, so only
trusted devices may join the tailnet (see
[ADR 0013](../ADR/0013-dashboard-on-the-tailnet.md)).
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
  A click edits `repos.yaml` at once (the file is written again from its parsed content, so comments are lost; the previous file is
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
See [ADR 0004](../ADR/0004-dashboard-edits-local-config.md),
[ADR 0052](../ADR/0052-workflows-read-model-presets-at-run-time.md) and
[ADR 0017](../ADR/0017-dashboard-edits-completion-notifications.md).

The Config view manages global presets in `model-presets.yaml` (version 1).
`light` covers `ask-pr/model`, `fix-ci/model`, `reply-pr/model` and
`review-pr/address_model`. `default` covers `_fix-issue-owned/model`, `_fix-issue-external/model`, `tend-repo/model`
and `review-pr/review_model`. A role can set an optional custom override; an
override wins over its preset.

A repository routed to `provider: claude` cannot use those IDs: the `claude`
CLI takes `sonnet`, `haiku` or `claude-sonnet-4-5`, not `provider/model`. So
the file holds a second pair, `claude_default` (`review-pr/claude_review_model`)
and `claude_light` (`review-pr/claude_address_model`), with their own syntax.
`review-pr` picks the pair that matches its `provider` input.

Workflows read the file when a run starts (`bin/model`), so a saved change
applies to the next run without restarting awf or OpenCode, and nothing else is
written. The Run dialog still overrides one run, and a running request keeps its
started model. A `tend-repo` override does not change the resolved roles of its
nested `review-pr` run. See [ADR 0052](../ADR/0052-workflows-read-model-presets-at-run-time.md).

Prices come from awf itself: since v0.12.0 it records what each agent step cost
(`cost_usd`) in the run log, and the page sums that per run. A run started by an
older awf has no price and shows none.

Ask starts `workflows/ask-pr.yaml` with the message as input. The conversation
is a file, so every turn remembers the previous ones, and the agent reads what
is already on disk (`state/replies/pr<N>/`) before the fresh GitHub data. It
may write only its answer: the workflow makes no clone and the prompt forbids
commits, pushes and comments. Nothing feeds back into a verdict by itself. See
[ADR 0007](../ADR/0007-ask-the-agent-about-a-pull-request.md).

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

## Language

The page is in English and French. Settings, Appearance, Language picks `auto` (the browser's language), `en` or `fr`; the choice is kept in this browser (`dots-lang`) and reloads the page. Every visible string is a key in `ui/locale-en.js`, with its French in `ui/locale-fr.js`; code calls `t("key", [args])` from `ui/i18n.js`, and a missing key falls back to English. A new language is one more `ui/locale-xx.js` registered in `i18n.js`. The files are flat because the dashboard serves only flat assets and `ui/views/*.js`.
