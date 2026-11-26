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

## On a timer

`make health` builds `bin/health` and runs it. `systemd/dots-health.timer`
starts `dots-health.service` 2 minutes after boot and every 10 minutes after
that. The service writes the `--json` result to `$STATE_DIR/health.json`
(replaced whole, so a reader never sees half a file) and keeps the exit code of
`bin/health`. Exit 1 (degraded) is recorded but does not fail the unit; exit 2
(down) fails it. The token is read from `.env` by the command and never printed.

`OnFailure=` starts `dots-health-alert.service`, which posts one line to the
`DISCORD_WEBHOOK` of `.env`, the destination the workflow `notify` steps use. It
does nothing when the variable is unset. The dashboard's `notifications.json`
(ADR 0017) is a separate destination that only the dashboard process reads, so
the alert does not use it.

```bash
cp systemd/dots-health*.* ~/.config/systemd/user/
systemctl --user daemon-reload
systemctl --user enable --now dots-health.timer
```
