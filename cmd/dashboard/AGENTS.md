# cmd/dashboard/

The activity page over `state/`, a reverse proxy to the awf API, and the editor
for local config files. One Go package, `main`: `main.go` holds the shared
types, `main()` and the route wiring; handlers sit in `runs.go`, `cost.go`,
`timeline.go`, `ask.go`, `repos.go`, `models.go` (HTTP glue only; the logic is in
`../../internal/models`), `notifications.go`, `files.go`, `security.go`,
`util.go`, with a `*_test.go` per file. The frontend is `../../ui/`, served from
disk as ES modules, with no build step.

## WHAT IT SERVES

| Route | Reads or writes |
| --- | --- |
| `/` and `/app.js`, `/views/*.js`, `/*.css` | `ui/` assets (`-ui` names the HTML file) |
| `/api/artifacts` | `state/reviews/`, `state/replies/`, `state/awf-gh/seen.jsonl` |
| `/api/file?path=` | one artifact file, under `reviews/` or `replies/` only |
| `/api/log?id=` | `state/logs/<id>.jsonl` |
| `/api/cost?id=&from=&to=` | what opencode charged for one run |
| `/api/costs?from=&to=` | every opencode session in a window, for the history table |
| `/api/usage` | `state/logs/index.jsonl` plus each run's log: what each pull request cost and which workflow spent it |
| `/api/stream?id=` | tails the run: awf's transcript when present, else `state/logs/<id>.jsonl` |
| `/api/logs` | `state/logs/index.jsonl`: which execution recorded which run, plus its price, target and archived `rounds` |
| `/api/repos` | `awf-gh.yaml`, the seen list, and `repos.map` for the Tower check |
| `/api/repos/names` | repository names for the run dialog, from the configs and `state/tend` |
| `/api/replay?id=` | what a finished run was given: workflow, scope and inputs |
| `/api/thread?repo=&pr=` | the Ask conversation for one pull request |
| `/api/ask` (POST) | appends the question to the thread and starts `ask-pr` |
| `/api/ask/answer` (POST) | appends `answer.md` to the thread and deletes the file |
| `/api/memory` | `state/tend/<owner>/<repo>/{lessons,memory}.md` and `since` |
| `/api/files` | the editable files: `workflows/*.yaml` and the local configs |
| `/api/source?path=` | one editable file as text |
| `/api/save` (PUT) | writes one editable file, keeps a `.bak` |
| `/api/models` (GET) | lists workflow model input variables, saved defaults, and available local model IDs |
| `/api/models` (PUT) | updates named workflow model input defaults |
| `/api/notifications` (GET) | reads the Discord webhook setting from `notifications.json`, or the disabled defaults |
| `/api/notifications` (PUT) | saves the Discord webhook setting under the same write lock and keeps a `.bak` |
| `/awf/*` | proxied to `AWF_URL`, default `http://127.0.0.1:2511` |

## MODEL DEFAULTS

The Config view's Model defaults form lists variables with `GET /api/models` and
saves them with `PUT /api/models`. Single-agent workflows use `model`.
`review-pr` uses `review_model` and `address_model`. A model identifier is
`provider/model`, with an optional `#variant`. The `provider: opencode` field
selects the awf adapter, not the model provider. `GET /api/models` also returns
an `available` array from the local `opencode models` command. The Config view
offers those IDs in a native datalist and keeps every field free text. A failed
or timed-out lookup returns an empty array without blocking the form.

Automatic runs from `awf-gh`, Ask and `bin/tend` omit model inputs and use the
saved defaults. The Run dialog can override a model for one run. A saved change
applies to the next run without restarting awf or OpenCode. A running request
keeps the model it started with.

Only a dashboard-started run keeps its exact original inputs in browser
`localStorage`, including a model override. Replay uses that record when
available. An automatic run has no browser record, so replay may use the current
saved default instead of the historical model.

A `tend-repo` model override changes only its own agent step. It does not replace
the `review_model` or `address_model` used by the nested `review-pr` run.

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

## ASK

The Ask button on a pull request card opens a conversation. One message is one
run of `workflows/ask-pr.yaml`, whose agent step reads `thread.md` and writes
`answer.md`; the page then appends the answer and deletes the file, so a turn is
counted once.

- The thread lives in `state/asks/<owner>/<repo>/pr<N>/`: `thread.json` for the
  page, `thread.md` for the agent, both written by `writeThread`. awf keeps no
  session across runs, so the file is the memory of the conversation.
- The repository of an artifact comes from `reviews/<id>/repo` (written by
  `review-pr`'s fetch step) or from `replies/<id>/pr.json`'s `url`. An artifact
  older than that has no repository, so it gets no Ask button.
- `ask` checks, in order: `POST`, loopback `Host` and `Origin`, JSON content
  type, 16 KiB cap, `repoNameRE`, `prNumberRE`, non-empty question. A start that
  awf refuses rolls the question back, so the thread never keeps a message that
  was not answered.
- An answer may end with `SUGGEST: <workflow> k=v` lines. `parseSuggest` removes
  every such line from the displayed text (even a malformed one), keeps at most
  three, refuses names outside `[A-Za-z0-9_-]+`, and stores them on the message
  as `suggest`. The page renders one button per entry; the button opens the run
  dialog with `repo` and `pr` filled in, so nothing starts without a click.
- `/api/ask/answer` is idempotent per `run_id`: a page that reloads mid-run
  appends nothing twice.

## RULES

- Bind `127.0.0.1` only. The default is `-addr 127.0.0.1:8099`. There is no
  authentication: the loopback bind is the boundary.
- The tailnet reaches the page through `tailscale serve` on
  the tailnet address of this machine (see `systemd/dots-dashboard.env`), never through a wider bind. See
  ADR 0013.
- `/api/save` writes cover exactly four shapes: `workflows/<name>.yaml` (name
  matches `[A-Za-z0-9_-]+`), `awf-gh.yaml`, `tend.list`, `repos.map`.
  `resolveEditable` refuses `..` before cleaning, because `filepath.Clean` would
  fold it back into a valid name.
- `saveFile` checks, in order: `PUT`, trusted `Host` and `Origin`, JSON
  content type (a cross-site form cannot send it without a preflight), size cap
  (1 MiB), path allowlist, YAML parse. A YAML file that does not parse is
  refused and left untouched, because a broken watcher config stops the next
  polling pass.
- `PUT /api/models` checks `PUT`, trusted `Host` and `Origin`, JSON content type
  and a size cap before it updates workflow files. Match exact workflow and
  input names. Refuse unknown keys and duplicate input names.
- Refuse a structural conflict instead of overwriting it: the named model input
  must exist once, be an optional string, have a non-empty scalar default and
  carry the shared validation pattern. Each agent step must reference the
  expected model input. The generic YAML editor can break this convention, so
  the form reports the conflict.
- Model values must be non-empty and match `provider/model`, with an optional
  `#variant`. Validation checks identifier syntax, not provider availability.
  An unavailable model fails the next agent step.
- Edit only the matched `default` scalar. Preserve every other line and byte;
  never marshal the whole YAML document.
- Serialize `/api/models` updates with `/api/save` under the same write lock.
  Keep a stamped `.bak` and use `atomicWrite`, as `saveFile` does. A save across
  several workflow files is not one filesystem transaction, so a run that starts
  during the save can use mixed defaults.
- The Config view also edits the private `notifications.json` at the repository
  root through `GET` and `PUT /api/notifications`. Its only shape is
  `{"discord":{"enabled":boolean,"webhook":string}}`. GET returns the saved
  config, or `enabled: false` with an empty webhook when the file is absent.
- `/api/notifications` uses the same boundary as `/api/models`: trusted `Host`
  and `Origin`, JSON content type on PUT, a 16 KiB cap, one JSON object, and no
  unknown keys. A webhook is empty or a Discord URL of at most 512 characters.
- Writes to `notifications.json` take `editMu`, keep a stamped `.bak`, and use
  `atomicWrite`. The recorder reads the file at send time, so a Config save
  changes the next notice without a restart.
- The recorder sends one Discord notice per execution id when its log has
  `workflow.completed` or `workflow.failed`. The message names the workflow and
  that state. `notified` under `d.mu` deduplicates per run; `seedNotified()` marks
  the logs already on disk at startup so a restart sends no repeat, and
  `pruneLogs()` drops ids whose log has gone. A post has a five second timeout;
  a failure is logged and recording continues. The injectable `discordPost`
  field defaults to `postDiscord`.
- This is separate from `DISCORD_WEBHOOK` and the workflow `notify` steps. Do
  not add a webhook test endpoint: saving the config sends nothing.
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
- awf v0.12.0 records what each agent step cost (`cost_usd`) in the run log, so
  `/api/cost` sums that over the transcript, falling back to the recorder file.
  `/api/usage` adds it up per pull request, taking the pull request from
  `replayInputs` over the same log, so a run whose prompt names no pull request
  counts under "Unattributed runs". That grouping is a guess from the prompt
  text: it never reads GitHub.
  The sum is memoised by file size and modification time, because the page asks
  on every refresh. `/api/logs` carries the same number per row, so the history
  table needs no second request. It also carries the run's `repo` and `pr` or
  `issue`, parsed from the log by `cacheTargets` and kept by log version. A run
  from an older awf has no price.
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
- A write needs its `Host`, and its `Origin` when present, to be loopback or
  a name from `-allow-host` (`DOTS_ALLOW_HOST` in the unit). `trustedRequest`
  makes that test; the list exists only for the tailnet name the proxy
  forwards.
- `/api/repos` sets `tower_ignore` ("missing" or "empty") only when the
  opencode config declares a tower MCP server that is not disabled, and the
  repository's checkout in `repos.map` has a `.towerignore` that is absent or
  lists no pattern. The Repos tab renders it as a warning; Tower indexes every
  non-hidden file without one.

## FLAGS

`-addr`, `-state` (default `STATE_DIR`), `-root` (default the repository root;
holds `workflows/` and the local configs), `-allow-host` (default
`DOTS_ALLOW_HOST`), `-awf` (default `AWF_URL`), `-ui`,
`-awfgh` (default `awf-gh.yaml`), `-opencode` (default the opencode config under
`$XDG_CONFIG_HOME`).

## NOTES

- `projectRoot` assumes the binary sits in `bin/`. Running `go run` from another
  directory falls back to the working directory.
- Tests cover the index, `resolveEditable`, the save handler (backup, YAML
  refusal, origin and path rejection), and the loopback and trusted-host checks.
  Model API tests cover GET and PUT validation, bad requests, exact-key changes,
  structural conflicts, quote, comment and line-ending preservation, backups,
  unchanged-request idempotence, multi-file preflight, and rollback after a write
  failure. `TestWorkflowModelInputs` checks all six workflows for optional model
  inputs with non-empty defaults, the shared validation pattern, and the correct
  agent references. Notification tests cover defaults, validation, origin and
  size rejection, backups, one notice per execution, and the Discord payload
  through an injected post function.
- `state/logs/` only holds runs that happened while the page was up.
  `systemd/dots-dashboard.service` keeps it up for that reason.
