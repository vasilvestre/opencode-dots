# cmd/dashboard/

The activity page over `state/`, a reverse proxy to the awf API, and the editor
for local config files. One Go file, `main.go`, package `main`. The frontend is
`../../ui/`, served from disk as ES modules, with no build step.

## WHAT IT SERVES

| Route | Reads or writes |
| --- | --- |
| `/` and `/app.js`, `/views/*.js`, `/*.css` | `ui/` assets (`-ui` names the HTML file) |
| `/api/artifacts` | `state/reviews/`, `state/replies/`, `state/awf-gh/seen.jsonl` |
| `/api/file?path=` | one artifact file, under `reviews/` or `replies/` only |
| `/api/log?id=` | `state/logs/<id>.jsonl` |
| `/api/cost?id=&from=&to=` | what opencode charged for one run |
| `/api/costs?from=&to=` | every opencode session in a window, for the history table |
| `/api/stream?id=` | tails the run: awf's transcript when present, else `state/logs/<id>.jsonl` |
| `/api/logs` | `state/logs/index.jsonl`: which execution recorded which run |
| `/api/repos` | `awf-gh.yaml`, the seen list, and `repos.map` for the Tower check |
| `/api/repos/names` | repository names for the run dialog, from the configs and `state/tend` |
| `/api/replay?id=` | what a finished run was given: workflow, scope and inputs |
| `/api/memory` | `state/tend/<owner>/<repo>/{lessons,memory}.md` and `since` |
| `/api/files` | the editable files: `workflows/*.yaml` and the local configs |
| `/api/source?path=` | one editable file as text |
| `/api/save` (PUT) | writes one editable file, keeps a `.bak` |
| `/awf/*` | proxied to `AWF_URL`, default `http://127.0.0.1:2511` |

## REPLAY

`/api/replay` feeds the Replay button on a finished run. awf keeps no copy of
the inputs, so the values are recovered twice:

1. from the run log: every workflow prompt spells `owner/repo#123`, and a review
   run also writes `pr123-<sha>`. `replayInputs` reads them back, which is a
   guess and the page says so.
2. from the page itself: when a run starts from the run dialog, `ui/run.js`
   keeps its exact inputs in `localStorage` under `dots-run-<id>`. That copy
   wins, and it only exists in the browser that started the run.

If a prompt changes shape, the guess degrades to the fields it still finds and
the operator fills the rest. Nothing here is written to disk.

## RULES

- Bind `127.0.0.1` only. The default is `-addr 127.0.0.1:8099`. There is no
  authentication: the loopback bind is the boundary.
- Writes cover exactly four shapes: `workflows/<name>.yaml` (name matches
  `[A-Za-z0-9_-]+`), `awf-gh.yaml`, `tend.list`, `repos.map`. `resolveEditable`
  refuses `..` before cleaning, because `filepath.Clean` would fold it back
  into a valid name.
- `saveFile` checks, in order: `PUT`, loopback `Host` and `Origin`, JSON
  content type (a cross-site form cannot send it without a preflight), size cap
  (1 MiB), path allowlist, YAML parse. A YAML file that does not parse is
  refused and left untouched, because a broken watcher config stops the next
  polling pass.
- The page never runs git. The backup file is the safety net for the private
  files; for tracked files, git is.
- The only other write is `state/logs/`: the watcher goroutine records a
  running execution's event stream, because awf forgets it once the run leaves
  its session registry. It prunes logs older than 30 days.
- `/api/stream` prefers awf's per-run transcript (`<data dir>/awf/transcripts/<id>.jsonl`),
  which also holds the agent messages the event stream omits. The recorder's
  file stays the fallback and the source of the terminal state, because the
  transcript has no terminal record. Both are trusted local files: they contain
  pull request text, so the listener stays on loopback.
- The page must not open awf's own event stream. awf gives the event channel to
  a single reader, and that reader is the recorder here: a second one would take
  half the events from it. `/api/stream` tails the recorded file instead.
- awf parses the agent price out of opencode's stream and drops it, so
  `/api/cost` reads opencode's database instead (`session_v2`, older `session`),
  matching sessions by the working directory awf runs agents in (`state/`) and
  by the run's time window. `/api/costs` returns the raw sessions so the page
  attributes each to one history row; a run can overlap its neighbour. It goes
  through the `sqlite3` binary: no Go driver. The image installs that binary.
  Without that binary or database the page shows no price.
- awf history ids differ from execution ids, so `state/logs/index.jsonl`
  records workflow and start time for every kept log. The page joins a history
  row to its log on workflow plus start second; `/api/executions` lists running
  runs only, so the index is the only source for finished ones.
- Proxy `/awf/*` same-origin instead of calling awf from the browser. That is
  why there is no CORS. Validation and run go through this proxy untouched:
  the page gains no privilege over awf.
- `logID` keeps an id from reaching the filesystem as a path: 8 to 64 chars,
  alnum and `-` only.
- `dash.file` cleans the path, whitelists the first segment (`artifactDirs`),
  whitelists the extension (`artifactExt`), then checks the result stays under
  `state/`. Keep all four checks in that order.
- Artifact reads stop at `reviews/` and `replies/`: `state/` also holds git
  clones and must not be walkable.
- Each handler answers JSON with `Cache-Control: no-store`.
- `/api/repos` sets `tower_ignore` ("missing" or "empty") only when the
  opencode config declares a tower MCP server that is not disabled, and the
  repository's checkout in `repos.map` has a `.towerignore` that is absent or
  lists no pattern. The Repos tab renders it as a warning; Tower indexes every
  non-hidden file without one.

## FLAGS

`-addr`, `-state` (default `STATE_DIR`), `-root` (default the repository root;
holds `workflows/` and the local configs), `-awf` (default `AWF_URL`), `-ui`,
`-awfgh` (default `awf-gh.yaml`), `-opencode` (default the opencode config under
`$XDG_CONFIG_HOME`).

## NOTES

- `projectRoot` assumes the binary sits in `bin/`. Running `go run` from another
  directory falls back to the working directory.
- Tests cover the index, `resolveEditable`, the save handler (backup, YAML
  refusal, origin and path rejection) and the loopback check.
- `state/logs/` only holds runs that happened while the page was up.
  `systemd/dots-dashboard.service` keeps it up for that reason.
