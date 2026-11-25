# 0017. The dashboard edits completion notifications

- Status: accepted
- Date: 2026-11-25
- Supersedes: 0004
- Superseded by: none

## Context

Record 0004 let the dashboard edit a fixed set of local files. Record 0014
added a second private edit through `/api/models`: the Config view reads and
writes workflow model defaults on the same host, with the same checks, without
restarting anything. The page is now the place an operator changes settings
that used to need a file edit.

`review-pr` and `reply-pr` already post to Discord from shell `notify` steps,
using `DISCORD_WEBHOOK` from `.env`. Those messages cover pull request work
only. The other workflows finish without a message, and changing the channel
means editing `.env` and restarting the services that read it. The operator
wants one notice for every finished run and wants to edit the destination on
the Config page.

## Decision

We add a private `notifications.json` at the repository root, with a tracked
`notifications.example.json`. Its only shape is
`{"discord":{"enabled":boolean,"webhook":string}}`. `GET /api/notifications`
returns the saved config or that shape with `enabled: false` and an empty
webhook. `PUT /api/notifications` validates and saves the same shape and
answers with it.

Both methods require a trusted `Host` and `Origin`, as `trustedRequest`
defines them. PUT also requires `Content-Type: application/json`, caps the body
at 16 KiB, accepts exactly one JSON object, and refuses unknown keys, a
non-boolean `enabled`, and a webhook that is neither empty nor a Discord
webhook URL of at most 512 characters. Writes hold `editMu`, keep a stamped
`.bak`, and use `atomicWrite`, as `/api/models` does.

The recorder sends one message when it sees `workflow.completed` or
`workflow.failed` in an execution's log. It reads the config from disk at that
moment, so a Config save takes effect without a restart. It posts
`{"content":"..."}` with the workflow name and terminal state, with a five
second timeout. A failed post is logged; it never stops recording. An in-memory
map keyed by execution id keeps one message per run. At startup the recorder
marks every log already on disk as announced, so a restart sends no notice for
a run the previous process saw. Pruning drops ids whose log has gone.

The workflow `notify` steps and `DISCORD_WEBHOOK` in `.env` stay as they are.
Completion notices are a second, independent destination.

## Consequences

Every finished run recorded while the dashboard is up gets one notice, with no
change to any workflow. The Config page holds the whole setting. A run that
ended before the recorder wrote its log, or before the dashboard started, gets
no notice. The dedup map lives in the process and is seeded from `state/logs/`
at startup, so a restart does not repeat notices.

The webhook URL now lives in a gitignored file as well as, separately, in
`.env`. Both are local, and stamped backups of the new file are gitignored too.
The write boundary is unchanged and there is no webhook test endpoint: a save is
checked for shape, not for reachability.

## Alternatives

- Reuse `DISCORD_WEBHOOK` from `.env`: one setting for both channels, but a
  change needs a restart and a dashboard edit would reach into the service
  environment.
- Add a `notify` step to every workflow: shell steps own side effects (ADR
  0002), but this duplicates five steps and still leaves no way to edit the
  destination from the Config page.
- Persist the dedup map under `state/`: it would survive a restart, at the cost
  of another file to prune for a notice awf already forgets.
