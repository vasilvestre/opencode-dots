# systemd units: one manifest per role

A machine runs a few long services and **one** timer. `bin/dots-units` installs them from a
manifest by role, and `bin/tick` runs every short recurring job from a task file.

```bash
bin/dots-units install --role workstation --prune   # or server, or worker
bin/dots-units status                               # what is missing, off, extra or obsolete
```

| Role | Services | Timers |
| --- | --- | --- |
| `workstation` | `dots-awf`, `dots-dashboard`, `dots-opencode`, `dots-worker` (once `worker.env` exists: this machine then also works for a server) | `dots-tick`; `dots-tend` only with `DOTS_ENABLE_TEND=1` (or if already on) |
| `server` | the same, and `dots-hub` once `mercure` and `hub.env` exist | `dots-tick`; `dots-tend` only with `DOTS_ENABLE_TEND=1` (or if already on) |
| `worker` | `dots-awf`, `dots-worker` (once `~/.config/dots/worker.env` sets `DOTS_WORKER_URL`) | `dots-tick` |

`install` writes the units with this checkout's path, saves the role in `~/.config/dots/role`,
reloads systemd and enables what the role runs. It never touches drop-ins or env files: a setting
(a client id, `DOTS_HUB_URL`) stays in `~/.config/systemd/user/<unit>.d/` or in an env file.
`--prune` removes the units `bin/tick` replaced; `prune` alone does only that, and `--dry-run`
shows what either would do.

## bin/tick

`dots-tick.timer` runs `bin/tick` every minute. It reads `deploy/tick/<role>.tasks`, one line per
task: `NAME EVERY TIMEOUT COMMAND`. A task that is due runs beside the others; its last start and
exit code go to `STATE_DIR/tick/`. A task never run, or due while the machine was off, runs once
when the machine is back (the catch-up `Persistent=true` gave each old timer). A task still
running is left alone, and a failure never stops another.

```bash
bin/tick list         # every task, last run, how it ended
bin/tick run health   # run one now, show its output
```

| Task | Every | What it does (the old unit) |
| --- | --- | --- |
| `claim` | 1 min | renew the branch claims this machine holds (`dots-claim`) |
| `health` | 10 min | write `state/health.json`, tell Discord when down (`dots-health`) |
| `breaker` | 15 min | `bin/breaker check-all` (`dots-breaker`) |
| `sync` | 5 min | fast-forward managed clones (`dots-sync`) |
| `workspace-sync` | 15 min | pull or publish the workspace memory (`dots-workspace-sync`) |
| `awf-gh` | 2 min | one pass of every `awf-gh*.yaml` watcher (`dots-awf-gh`, `dots-awf-gh-executor@`) |
| `backup-server` | 1 day | pull a backup of the server, when `executor.env` sets `DOTS_SERVER` (`dots-backup-server`) |

`dots-tend` stays its own daily timer: a pass can last hours and must not share a lock with a
one-minute tick. `dots-executor-tunnel` and `dots.service` (the Docker stack) are no longer
installed: the queue (docs/remote.md) replaced the reverse tunnel.

Tests: `bin/test-tick`, `bin/test-dots-units`.
