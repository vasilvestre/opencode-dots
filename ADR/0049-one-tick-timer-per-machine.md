# 0049. One tick timer per machine, installed by role

- Status: accepted
- Date: 2026-11-27
- Supersedes: none
- Superseded by: none

## Context

Each short recurring job had its own `.service` and `.timer`: claims, health, the circuit breaker, the
managed clones, the workspace memory, the GitHub watchers and the server backup. A machine ended with more
than ten units to install, keep in step and clean up, and no two machines had the same set. A unit installed
by hand as a link into the repository once emptied its own source when a script wrote through it, and the
Health page stayed empty for days because nothing installed its timer.

## Decision

We run every short recurring job from one timer, and install a machine from one manifest by role.

`dots-tick.timer` runs `bin/tick` every minute. It reads the machine's task file
(`deploy/tick/<role>.tasks`: `NAME EVERY TIMEOUT COMMAND`) and runs what is due, each task beside the
others with its own lock, a timeout, and its last start and exit code under `STATE_DIR/tick/`. A task that
was never run, or was due while the machine was off, runs once when it is back (the catch-up each timer
used to get from `Persistent=true`). A failure stops nothing else. The daily `tend` pass stays its own
timer: it lasts hours and spends tokens, so it is opt-in (`DOTS_ENABLE_TEND=1`).

`bin/dots-units` installs, checks and prunes the units of a role (`workstation`, `server`, `worker`) from
one manifest. It writes each unit to a temporary file and replaces the target, never writing through a
link; it leaves a unit the user masked alone, leaves opencode to whoever already serves its port, finishes
the install when one unit cannot start and says so in its exit code, and removes the units the tick
replaced. Settings of a unit stay in drop-ins and env files, never in the manifest.

## Consequences

- A machine has a handful of long services, one timer and the daily pass; `bin/dots-units status` says what
  is missing, off, extra or obsolete, and `bin/tick list` says how each job ended.
- A job's schedule is a line in a file in the repository, not a unit.
- Jobs are sequenced by a script of ours, not by systemd: a minute is the finest grain, and a job that is
  due while `tick` waits on a long one is started beside it, not after it.
- The jobs a role runs are reviewed in one place; the units that remain are the ones that must stay up.

## Alternatives

- **ztick** ([the awf scheduler](https://docs.awf-project.ai/ztick/)). Push-driven and one-shot: no
  recurrence, and no documented catch-up after a stop; it would add a daemon instead of removing units. It
  may fit later for delayed one-off actions (recheck a repository a day after a merge).
- **Keep one timer per job.** The friction this record removes.
- **A supervisor process in Go.** More to build for what a loop and `flock` do.
