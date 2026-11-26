# Circuit breaker

ADR 0040. At `auto` the dot merges its own pull requests. The breaker is the safety valve: when a
merge made by the dot goes wrong, the repository drops to `act` (pull requests and comments, no
merges) until a person raises it again. It is off unless asked for.

```yaml
repos:
  acme/app:
    autonomy: auto
    circuit_breaker: true              # or:
    # circuit_breaker:
    #   subjects: [security, db/migrate/]
```

`circuit_breaker` is refused unless the effective autonomy is `auto` (explicit, or derived from
`tend.auto_merge`): below it the dot never merges and there is nothing to break. The rules receive
an input `breaker`: `on`, or `on:` followed by the subjects joined by commas.

## What trips it

`bin/breaker check OWNER/REPO [SUBJECTS]` looks at the pull requests merged in the last
`BREAKER_DAYS` (default 7) by the dot (its login is the merge actor or the author, `DOT_LOGIN`, else
the login `gh` is signed in as) and trips on the first of:

| Reason | Trigger |
| --- | --- |
| `revert` | a later merged pull request titled `Revert ...` that names the merge (`#N` in title or body, or its title) |
| `red-check` | a check run of the merge commit ended `failure` or `timed_out` |
| `subject` | a merge touches a given subject: a label, or a path prefix of a changed file |

A subject trips on its own, without any failure. A direct `git revert` pushed without a pull
request is not seen. Only the merge commit's checks are read, not later commits.

Tripping writes `STATE_DIR/breaker/<owner>__<repo>.json` with the reason, the pull request, its URL and
the time. A repository already tripped is left alone.

## Effect

`bin/publish-gate` looks for that file for `GATE_REPO` (default: derived from `GATE_REF` when it is
`owner/repo#n`). When it exists, `auto` is read as `act` for that call and the reason goes to
stderr, so a merge is refused (exit 5) and comments, pushes and pull requests go on. Other
ceilings and the legacy empty ceiling are unchanged.

## Raising it again

```bash
bin/breaker status                 # repository, time, reason, pull request, detail
bin/breaker reset OWNER/REPO       # a person: removes the file, logs who and when
```

The reset is appended to `STATE_DIR/breaker/log.txt` (`BREAKER_WHO`, default the unix user) and
stamps `<owner>__<repo>.since`: only merges after it are looked at, so the revert that tripped the
breaker does not trip it again.

## Running it

`bin/breaker check-all` reads `BREAKER_LIST` (default `breaker.list`), one `owner/repo` per line,
optionally `owner/repo=subject,subject`, `#` for comments, and runs `check` on each. A timer runs
it; see `systemd/`. Nothing generates that list yet.

`bin/test-breaker` covers every trigger against a fake `gh`.
