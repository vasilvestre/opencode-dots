# cmd/dashboard/

Read-only page over `state/` plus a reverse proxy to the awf API. One file,
`main.go`, package `main`. The frontend is `../../ui/index.html`, served from
disk, with no build step.

## WHAT IT SERVES

| Route | Reads |
| --- | --- |
| `/` | `ui/index.html` (`-ui` flag) |
| `/api/artifacts` | `state/reviews/`, `state/replies/`, `state/awf-gh/seen.jsonl` |
| `/api/file?path=` | one artifact file, under `reviews/` or `replies/` only |
| `/api/log?id=` | `state/logs/<id>.jsonl` |
| `/api/repos` | `awf-gh.yaml` plus the seen list |
| `/api/memory` | `state/tend/<owner>/<repo>/{lessons,memory}.md` and `since` |
| `/awf/*` | proxied to `AWF_URL`, default `http://127.0.0.1:2511` |

## RULES

- Bind `127.0.0.1` only. The default is `-addr 127.0.0.1:8099`.
- The only write is `state/logs/`: the watcher goroutine records a running
  execution's event stream, because awf forgets it once the run leaves its
  session registry. It prunes logs older than 30 days.
- Proxy `/awf/*` same-origin instead of calling awf from the browser. That is
  why there is no CORS.
- `logID` keeps an id from reaching the filesystem as a path: 8 to 64 chars,
  alnum and `-` only.
- `dash.file` cleans the path, whitelists the first segment (`artifactDirs`),
  whitelists the extension (`artifactExt`), then checks the result stays under
  `state/`. Keep all four checks in that order.
- Artifact reads stop at `reviews/` and `replies/`: `state/` also holds git
  clones and must not be walkable.
- Each handler answers JSON with `Cache-Control: no-store`.

## FLAGS

`-addr`, `-state` (default `STATE_DIR`), `-awf` (default `AWF_URL`), `-ui`,
`-awfgh` (default `awf-gh.yaml`).

## NOTES

- `projectRoot` assumes the binary sits in `bin/`. Running `go run` from another
  directory falls back to the working directory.
- There is no test file today. The security-relevant functions to cover first
  are `logID`, `dedupeSeen` and the `dash.file` prefix check.
- `state/logs/` only holds runs that happened while the page was up.
  `systemd/dots-dashboard.service` keeps it up for that reason.
