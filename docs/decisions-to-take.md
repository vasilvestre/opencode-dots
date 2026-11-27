# Decisions to take

State on 2026-11-27. Every ADR is accepted or superseded except two that wait for a reading:
[0048](../ADR/0048-workers-pull-jobs-from-a-queue.md) (workers pull jobs from a queue, through one public
entry) and [0049](../ADR/0049-one-tick-timer-per-machine.md) (one tick timer per machine). Both describe what is
built and running; they stay `proposed` until the owner accepts them.

## Accepted on 2026-11-27

0029 (independent servers and contexts), 0036 (a daily USD cap per repository, first step; payer, monthly
view and per-person cap are out of this record), 0043 (the dot files issues upstream at `auto`, with a mandatory
visible line), 0047 (the owner is whoever reaches the dashboard on loopback). 0033 (hybrid executors) is
superseded by 0048.

## Still open

| Question | Where | Recommended |
| --- | --- | --- |
| Does a viewer role exist before the first teammate? (0037) | roles are owner and visitor only | audit first, roles later |
| Shared lessons between servers (0045 says dots do not talk) | a person copies a rule by hand | wait |
| Which part of the engine is public and which is offered, if it is ever sold? | not decided | keep the sources, the runner and the workflows separable |
| Revision check: a stale executor refuses a run (from 0033) | not built | a small record of its own when a stale clone hurts |

## Things only the machine's owner can do

- **Install a machine:** `bin/dots-units install --role workstation|server|worker` (docs/units.md). The tick
  carries health, the breaker, the clones, the workspace memory, the watchers and the server backup.
- **Issue a worker token** for each machine that works for a server: `bin/worker-token issue LABEL --repos ...`
  on the server (docs/remote.md), then `~/.config/dots/worker.env` and `bin/dots-units install` on the worker.
- **Name the first upstream repositories** and their weekly caps under `upstream:` in `repos.yaml`.
- **Enable the daily tend pass** on a machine only if wanted: `DOTS_ENABLE_TEND=1 bin/dots-units install ...`.

## Not built, and why

- Roles and a viewer (0037): each changes what a person may do.
- Upgrade pull requests for advisories need `composer update` in a sandbox with network, a security decision.
- Emergency stop per repository, dry-run, first-contribution filters, retention and evidence export, a periodic
  report, moving a repository between dots: open tickets.
