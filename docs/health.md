# Is the dot alive?

`cmd/health` answers with one command and one exit code.

```bash
go build -o bin/health ./cmd/health
bin/health            # human summary
bin/health --json     # machine summary: {"overall": "...", "checks": [...]}
```

## What it checks

| Check | Fails or warns when |
| --- | --- |
| `awf` | `GET $AWF_URL/api/workflows` does not answer (default `http://127.0.0.1:2511`). Critical. |
| `executor <name>` | an executor of `repos.yaml` (`settings.executors`) does not answer. |
| `github token` | `GH_TOKEN` (environment, then `.env`) is missing or refused (critical); warns under 7 days left, read from the `github-authentication-token-expiration` header, when GitHub sends it; warns when GitHub is unreachable. |
| `unit <name>` | an enabled systemd user unit of the project is not `active`. Skipped when there is no user systemd. |
| `state dir` | `STATE_DIR` is not writable. Critical. |
| `disk free` | free space under `STATE_DIR` is below the threshold (default 1024 MiB). Critical. |
| `last run` | the newest file under `state/reviews`, `replies` or `tend` is older than 48 h, or none exists. |

The token is sent only to `api.github.com` and is never printed.

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | healthy |
| 1 | degraded: a warning or a non-critical failure |
| 2 | down: a critical check failed |

## Flags

`--dir` (repository root, default the working directory), `--state`, `--awf`,
`--json`, `--min-free-mb`, `--token-warn-days`, `--stale-after`, `--units`.

## On a schedule

`health` is a task of `bin/tick` ([units.md](units.md)): every 10 minutes `bin/health-check` runs
`bin/health --json` and writes the result to `$STATE_DIR/health.json` (replaced whole, so a reader never sees half a
file). Exit 1 (degraded) is recorded and is not a failure; exit 2 (down) makes the task fail and posts one line to the
`DISCORD_WEBHOOK` of `.env`, the destination the workflow `notify` steps use. It does nothing when the variable is
unset. The dashboard's `notifications.json` (ADR 0017) is a separate destination that only the dashboard process
reads, so the alert does not use it. The token is read from `.env` by the command and never printed.

```bash
bin/tick run health        # run it now and show its output
bin/tick list              # when it last ran and how it ended
```

`bin/dots-units install --role <role>` installs the tick, so there is nothing to enable one by one. `.env` must define:

- `GH_TOKEN`: the token the `github token` check tests (sent only to `api.github.com`).
- `DOT_LOGIN`: the dot's GitHub login, shared with the breaker ([breaker.md](breaker.md)).
- `DISCORD_WEBHOOK`: where the alert posts when the dot is down. Unset, the alert does nothing.

### What to check after

- `bin/tick list` shows `health` with a recent time and `exit 0` or `exit 1`.
- `bin/health` prints the summary and exits 0 (healthy), 1 (degraded) or 2 (down).
- `$STATE_DIR/health.json` exists and is fresh; the dashboard's Health page reads it, and says so when it is stale
  (over 30 minutes).
- `bin/breaker status` ([breaker.md](breaker.md)) if the breaker is on too.
