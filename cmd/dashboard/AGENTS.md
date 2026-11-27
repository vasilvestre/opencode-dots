# cmd/dashboard/

The activity page over `state/`, a reverse proxy to the awf API, and the editor
for local config files. One Go package, `main`: `main.go` holds the shared
types, `main()` and the route wiring; handlers sit in `runs.go`, `cost.go`,
`timeline.go`, `ask.go`, `repos.go`, `models.go` (HTTP glue only; the logic is in
`../../internal/models`), `notifications.go`, `files.go`, `security.go`,
`auth.go` and `visibility.go` (GitHub sign-in for visitors, `routes.go` wires them; the device flow, sessions and rights live in `../../internal/auth`),
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
| `/api/home` | the Home page: per repository the latest activity, runs and cost of 7 days, held and notes, memory flag; `home.go`, filtered per repository by `shapeHome` |
| `/api/stream?id=` | tails the run: awf's transcript when present, else `state/logs/<id>.jsonl` |
| `/api/remote-runs` | runs of the executors named in `repos.yaml` `settings.executors`, polled from their awf and cached in `state/executors/<name>.json` (`remote.go`); no log, the executor holds it |
| `/api/logs` | `state/logs/index.jsonl`: which execution recorded which run, plus its price, target, archived `rounds` and model inputs from awf state files |
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
| `/api/models` (GET) | lists presets, migration status, model variables with preset and override, and available local model IDs |
| `/api/models` (PUT) | updates presets and overrides, then projects resolved workflow defaults |
| `/api/autonomy` | `state/notes/`, `state/held/` and `state/held/released/` (what the autonomy ceiling kept), read only |
| `/api/autonomy/artifact?kind=&name=` | (notes, held or released) the artifact one gate file points at, as text, inside `state/` only |
| `/api/autonomy/release` (POST) | starts `publish-held` through awf with `{id}`, after checking the record; the dashboard never publishes |
| `/api/health` | `state/health.json` written by `bin/health --json`, with its modification time; read only |
| `/api/notifications` (GET) | reads the Discord webhook setting from `notifications.json`, or the disabled defaults |
| `/api/notifications` (PUT) | saves the Discord webhook setting under the same write lock and keeps a `.bak` |
| `/awf/*` | proxied to `AWF_URL`, default `http://127.0.0.1:2511` |
| `/auth/device/start`, `/auth/device/poll`, `/auth/logout` (POST), `/api/session` (GET) | GitHub sign-in of a visitor, only when `DOTS_GITHUB_CLIENT_ID` is set, else 404; see the section below |

## MODEL PRESETS

The Config view's Model defaults form uses `GET /api/models` and
`PUT /api/models`. The tracked root `model-presets.yaml` has `version: 1`, two
presets (plus two optional claude ones) and optional per-role overrides. The
fixed mapping is:

- `light`: `ask-pr/model`, `fix-ci/model`, `reply-pr/model`,
  `review-pr/address_model`.
- `default`: `_fix-issue-owned/model`, `_fix-issue-external/model`, `tend-repo/model`, `review-pr/review_model`.
- `claude_light`: `review-pr/claude_address_model`.
- `claude_default`: `review-pr/claude_review_model`.

The claude roles use `models.ClaudePattern` (no provider prefix), and every
other role uses `models.Pattern`; each variable carries a `provider`. A missing
claude preset takes `sonnet` or `haiku`. `review-pr` agent steps read
`{{.states.pick_review_model.Output}}` and `{{.states.pick_address_model.Output}}`,
and the dashboard reports a conflict when a pick step does not read its three
inputs. The effective review and address models must differ for each provider.
See ADR 0019.

An override wins over its assigned preset. The API eagerly projects each
resolved ID into the workflow input's concrete default. awf keeps reading
workflow defaults, not the preset file. A model identifier is `provider/model`,
with an optional `#variant`. The `provider: opencode` field selects the awf
adapter, not the model provider.

`GET /api/models` returns `presets`, `migration_required`, `variables`,
`problems` and `available`. Each variable carries its `preset`, `override` and
role-specific `conflict`. Top-level `problems` also reports structural or preset
file errors when no role variables exist. The `available`
array comes from the local `opencode models` command. The Config view offers
those IDs in a native datalist and keeps every field free text. A failed or
timed-out lookup returns an empty array without blocking the form.

`PUT /api/models` accepts `{presets, overrides}`. An override value is a string
or `null`; `null` clears it. The legacy `{variables}` payload remains a
compatibility path that creates explicit overrides. Only `/api/models` writes
`model-presets.yaml`; the generic editor does not expose it.

A missing preset file means migration mode. `GET` does not write during
migration. Derive `light` from the current `ask-pr/model` default and `default`
from the current `_fix-issue-owned/model` default. Make every differing role an
override, preserving all current runtime models. The first save creates the
preset file.

Automatic runs from `awf-gh`, Ask and `bin/tend` omit model inputs and use the
saved workflow defaults. The Run dialog can override a model for one run. A
saved change applies to the next run without restarting awf or OpenCode. A
running request keeps the model it started with.

Only a dashboard-started run keeps its exact original inputs in browser
`localStorage`, including a model override. Replay uses that record when
available. An automatic run has no browser record, so replay may use the current
saved default instead of the historical model.

A `tend-repo` override changes only its own role. Its nested `review-pr` run uses
`review-pr`'s own resolved `review_model` and `address_model`.

## REPLAY

`/api/replay` feeds the Replay button on a finished run. awf keeps no copy of
the inputs, so the values are recovered twice:

1. from the run log: every workflow prompt spells `owner/repo#123`, and a review
   run also writes `pr123-<sha>`. `spend.ReplayInputs` reads them back, which is a
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

## AUTONOMY AND HEALTH

`release.go` holds both. `POST /api/autonomy/release` takes `{"id": "<held file
name>"}` and checks, in order: `POST`, trusted `Host` and `Origin`, JSON content
type, 4 KiB cap, a gate file name (`autonomyNameRE`), not already under
`held/released/` (409), a regular file under `held/` (404). It then calls
`startRun("publish-held", inputs)`, with `id` and, for a `file-issue` record, `repo` and `upstream` (422 when `upstreamOf` cannot tell), the same path as Ask, never `awf run`, and
answers `{execution_id}`. awf refusing the run gives 502. The workflow moves the
record; the dashboard never does and holds no GitHub token (ADR 0040). awf's run
request takes inputs only, so `RELEASED_BY` cannot be set here: the log names the
account behind the workflow's token. `GET /api/autonomy` also lists `released`. `GET /api/autonomy/artifact` takes
`kind=released` too and falls back from `held/artifacts/` to
`held/released/artifacts/`, where `publish-held` moves the copy.
`GET /api/health` answers `{present:false}` for a missing or unparsable file,
else `overall`, `checks` and `mtime`; the page treats a file over 30 minutes old
as stale.

## VISITORS (ADR 0044 and 0046, docs/dashboard-auth.md)

Off unless `-github-client-id` (`DOTS_GITHUB_CLIENT_ID`) is set: `dash.auth` is nil, `scoped` and
`ownerOnly` return the handler itself, and nothing changes. On, the owner is a request directly on
loopback with no `Tailscale-User-Login` (`ownerRequest`); every other request is a visitor.

- `routes.go` wraps every read in `scoped(handler, rule)`: a `gate` refuses before the handler runs
  (a run by id, a thread, a file, an autonomy artifact: 404 as a missing record), a `shape` filters
  the JSON it answered (lists by `repo`, usage totals recomputed, links only when both items are
  visible, audit rows by the repository in the target). Everything that changes something, the
  config pages, `/api/workers`, `/api/health` and `/awf/` go through `ownerOnly`: 403 for a signed-in
  visitor, 401 for one who is not. Put `ownerOnly` inside `audited` so a refused write is a row.
- A new read endpoint keyed by repository needs a rule in `routes.go`: without one it reaches a visitor
  unfiltered. A new write endpoint needs `ownerOnly`.
- The device flow asks for the scope `repo`. In the poll handler the visitor's token is used once
  (`loginOf`, then `VisibleRepos` in `internal/auth/rights.go`: one aliased GraphQL `viewerPermission` call per 50
  repositories, REST `GET /repos/{r}` per repository when GraphQL fails) and dropped. A session holds
  the login, the set of visible repositories, an expiry of one hour (`sessionTTL`, `auth.Config.Now` is
  injectable) and a 256-bit id. Sessions persist in `STATE_DIR/sessions.json` (`internal/auth/sessionstore.go`): keyed by the SHA-256 of the
  cookie id (the id is never written), entries hold only login, visible set, expiry and creation, mode 0600, atomic
  write, debounced about 1 s, 1000 at most, a bad or wide-mode file starts empty, off when sign-in is off.
  Never store a token, a device code or a user code there; `TestTheFileHoldsNoSecret` scans for them. Dashboard tests read a session with `Auth.Lookup(cookie)`; the persistence tests live in `internal/auth`. Authorization is a lookup in that set (`viewer.can`);
  there is no per-request call to GitHub, no cache and no dot token. Anything not returned, any error
  and a total failure hide the repository. Never pass the token to a log, an error, a session field or
  an audit row; `TestDeviceFlowSignsAVisitorIn` scans for it.
- `min_permission` (`-min-permission`, `DOTS_MIN_PERMISSION`): `push` (default, write or better) or
  `pull` (read or better). An invalid value stops the start (`parseMinPermission`). `/api/session`
  reports it for the sign-in page.
- Tests run against a fake GitHub (`auth_test.go`); the GitHub base URLs are fields of `auth.Config`.

## TEAM LISTENER (docs/team-ui.md)

`-team-addr` (`DOTS_TEAM_ADDR`) starts a second listener for developers who reach the dashboard through an
SSH forward, whose tunnel ends on loopback where the owner is. `teamHandler` (`team.go`) wraps the same mux for
that listener only and marks the request context; `ownerRequest` is false for a marked request, so every
request there is a visitor whatever its address, `Host` or headers. Never infer it from the `Host` port.

- `checkTeamAddr` stops the start for a non-loopback IP (a host name too), no GitHub client id, or the owner's address.
- Host: any loopback host and port, else 403. A request with an `Origin` needs it to equal the `Host`.
- A new route must be listed in `TestNoRouteIsReachableAsOwnerThroughTheTeamListener` as visitor-readable
  or owner only; the test fails on an unlisted one and on an owner-only route that is not 401 or 403 there.
- Never forward or document forwarding the owner port; `deploy/types/local/team-access.sh` writes keys
  whose `permitopen` is the team port.

## RULES

- Bind `127.0.0.1` only. The default is `-addr 127.0.0.1:8099`. There is no
  authentication for the owner: the loopback bind is the boundary. A visitor through the tailnet
  signs in with GitHub when that is configured (above).
- The tailnet reaches the page through `tailscale serve` on
  the tailnet address of this machine (see `systemd/dots-dashboard.env`), never through a wider bind. See
  ADR 0013.
- `/api/save` writes cover `workflows/<name>.yaml` (name matches
  `[A-Za-z0-9_-]+`), `repos.yaml`, and the four files generated from it
  (`awf-gh.yaml`, `tend.list`, `repos.map`, `sync.map`). `resolveEditable`
  refuses `..` before cleaning, because `filepath.Clean` would fold it back into
  a valid name.
- `repos.yaml` is validated by `repos.Build` before it is written: a file that
  would not generate is refused with 422 and the disk keeps the last good one.
  After the write, the generated files are rewritten (only the ones whose
  content changed) and the response lists them under `generated`. A hand edit of
  a generated file is refused with 409 while `repos.yaml` exists, because the
  next save of `repos.yaml` would overwrite it; before the migration it is
  allowed as it always was. `/api/files` flags them `generated`. ADR 0022.
- `saveFile` checks, in order: `PUT`, trusted `Host` and `Origin`, JSON
  content type (a cross-site form cannot send it without a preflight), size cap
  (1 MiB), path allowlist, YAML parse. A YAML file that does not parse is
  refused and left untouched, because a broken watcher config stops the next
  polling pass.
- `GET /api/models` uses the trusted `Host` and `Origin` check.
  `PUT /api/models` checks `PUT`, those trusted headers, JSON content type and a
  size cap before it writes. Parse the preset file as version 1 YAML. Refuse
  unknown versions, unknown keys and invalid YAML.
- Match exact workflow and input names in `variables`, `presets` and
  `overrides`. Refuse unknown keys and duplicate input names.
- Preset IDs and overrides must be non-empty and match `provider/model`, with an
  optional `#variant`. Reuse the model-ID validator and length limit. Validation
  checks identifier syntax, not provider availability. An unavailable model
  fails the next agent step. Reject equal effective `review-pr/review_model` and
  `address_model` values.
- Refuse a structural conflict instead of overwriting it: the named model input
  must exist once, be an optional string, have a non-empty scalar default and
  carry the shared validation pattern. Each agent step must reference the
  expected model input. The generic YAML editor can break this convention, so
  the form reports the conflict.
- Report drift when a workflow default differs from its resolved preset or
  override. Block the next preset save until the operator reconciles it.
- Edit only the matched workflow `default` scalar. Preserve every other line and
  byte; never marshal the whole YAML document.
- Prepare workflow scalar replacements and serialized preset content as one
  save plan. Serialize `/api/models` updates with `/api/save` under the same
  write lock. Write workflow files before `model-presets.yaml`. Keep a stamped
  `.bak` for each existing changed file and use `atomicWrite`. First-time preset
  creation and unchanged saves create no backups. If a later write fails, roll
  back written workflows and remove a newly created preset file. A multi-file
  save is not one filesystem transaction, so a run that starts during the save
  can use mixed defaults.
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
- Apart from files written through the editors, the only other write is
  `state/logs/`: the watcher goroutine records a running execution's event
  stream, because awf forgets it once the run leaves its session registry. It
  prunes logs older than 30 days.
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
  `spend.ReplayInputs` over the same log, so a run whose prompt names no pull request
  counts under "Unattributed runs". That grouping is a guess from the prompt
  text: it never reads GitHub. A run's repository is taken first from the
  `repo` input in awf's state file (`targetOf` in `timeline.go`), then from the
  first user message's own template line (`spend.PromptRepo`: `repo: owner/name`
  as the first line, or the `tend-repo` opening sentence), and only then guessed
  from text. The first two beat the guess (`spend.SetRepo` drops a pull request
  that belonged to the guessed repository); free text never sets a repository,
  so a daily-pass run groups under its repository and a visitor can see it.
  `/api/home` counts per repository from the same `targetOf`; a run with no
  repository is in no row.
  The sum is memoised by file size and modification time, because the page asks
  on every refresh. `/api/logs` carries the same number per row, so the history
  table needs no second request. It also carries the run's `repo` and `pr` or
  `issue`, parsed from the log by `cacheTargets` and kept by log version. A run
  from an older awf has no price. It also carries `models`, the model inputs
  read from the run's awf state file when that file exists.
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
holds `workflows/`, the local configs and `model-presets.yaml`), `-allow-host`
(default `DOTS_ALLOW_HOST`), `-awf` (default `AWF_URL`), `-ui`,
`-awfgh` (default `awf-gh.yaml`), `-opencode` (default the opencode config under
`$XDG_CONFIG_HOME`), `-github-client-id` (default `DOTS_GITHUB_CLIENT_ID`; empty turns visitor
sign-in off), `-github-url` and `-github-api` (defaults `DOTS_GITHUB_URL`, `DOTS_GITHUB_API_URL`),
`-min-permission` (default `DOTS_MIN_PERMISSION`, `push` or `pull`), `-team-addr` (default `DOTS_TEAM_ADDR`, empty off).

## NOTES

- `projectRoot` assumes the binary sits in `bin/`. Running `go run` from another
  directory falls back to the working directory.
- Tests cover the index, `resolveEditable`, the save handler (backup, YAML
  refusal, origin and path rejection), and the loopback and trusted-host checks.
  Model API tests cover validation, bad requests, exact-key changes, structural
  conflicts, quote, comment and line-ending preservation, backups, idempotence,
  multi-file preflight and rollback. Preset tests cover migration, resolution,
  drift, equal-role rejection, override clearing, creation, workflow-first
  ordering and API propagation. `TestWorkflowModelInputs` checks all six
  workflows for optional model inputs with non-empty defaults, the shared
  validation pattern and the correct agent references. Notification tests cover defaults, validation, origin and
  size rejection, backups, one notice per execution, and the Discord payload
  through an injected post function.
- `state/logs/` only holds runs that happened while the page was up.
  `systemd/dots-dashboard.service` keeps it up for that reason.
