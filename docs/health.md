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

A unit that fails when the dot is not healthy makes `systemctl --user --failed`
and any `OnFailure=` hook report it.

```ini
# ~/.config/systemd/user/dots-health.service
[Unit]
Description=Is the dot alive?

[Service]
Type=oneshot
WorkingDirectory=%h/projects/perso/opencode-dots
ExecStart=%h/projects/perso/opencode-dots/bin/health
SuccessExitStatus=1
# SuccessExitStatus=1 tolerates "degraded"; remove it to fail on warnings too.

# ~/.config/systemd/user/dots-health.timer
[Timer]
OnCalendar=*:0/15
Persistent=true

[Install]
WantedBy=timers.target
```

```bash
systemctl --user daemon-reload
systemctl --user enable --now dots-health.timer
```

For a push alert, add `OnFailure=` pointing at a unit that sends your
notification; the health command itself sends nothing.
