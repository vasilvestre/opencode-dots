# End-to-end tests

`bin/test-e2e` runs the real workflow files (`workflows/*.yaml`) through the real
`awf`, offline. The other tests extract one shell step or run the gate alone;
this one runs a whole state machine, including `call_workflow`, the agent steps
and the publishing steps, and then asks what reached "GitHub".

```bash
bin/test-e2e                  # everything, about a minute
bin/test-e2e fix-issue        # one section: fix-issue classify review held baseline upstream
E2E_KEEP=1 bin/test-e2e held  # keep the scratch trees (paths are printed)
```

It needs `awf`, `jq` and `git`. It is not part of `make check` or CI (CI has no
`awf`); run it before changing a workflow or `bin/publish-gate`.

## How it is built

Each case is a fresh scratch tree. `awf` only runs a workflow from a directory
holding `.awf/workflows/`, so the case copies the workflows there and runs `awf`
from it (with `--storage` and `HOME` inside the case, so `~/.local/share/awf` is
never touched).

| Piece | Where | What it does |
| --- | --- | --- |
| fake `gh` | `test/e2e/fakes/gh` | appends every call to `gh.log`; answers `issue view`, `pr view`, `pr diff`, `pr checks`, `repo view`, `api user`, `api repos/...` from canned JSON (`--jq` is applied with the real `jq`); `pr create`, `issue comment`, `pr comment`, `pr merge` only log, and the body files they are given are copied to `posted/`; any other call is logged as `UNHANDLED` and fails the case |
| fake origin | `remotes/tester/demo.git` | a local bare repository. A private git config maps `https://github.com/` onto the scratch `remotes/`, so the workflows' own clone, fetch and push URLs hit it, and `GIT_ALLOW_PROTOCOL=file` forbids anything else. Real `git` is used, so a push is proven by the refs and the file contents on origin |
| stub agent | `test/e2e/fakes/opencode` | awf calls `opencode run PROMPT`; the stub reads the step from the prompt and writes the files that step may write: a classification (`FAKE_CLASS` 1, 2, 3, `bad`, `none`), a fix or a decline (`FAKE_FIX`), a review (`FAKE_REVIEW`), an address round. Which steps ran goes to `agent.steps` |
| fake `curl` | `test/e2e/fakes/curl` | fails and logs: no webhook can go out |
| fake `bin/held` | `test/e2e/fakes/held` | the claim check, exit code from `FAKE_HELD_RC` |
| `DOTS_HOME` | `dots/` | the real `bin/publish-gate`, the fake `held`, the real `workflows/data` |

The repository is `tester/demo` and the dot's login is `tester`: the owned path
of `fix-issue` and the ownership test of `review-pr` (`head owner == me`) both
hold, and a pull request by `stranger` is someone else's. No real name appears.

`test/e2e/lib.sh` holds the helpers (`case_new`, `seed_pr`, `run_wf`, `check`); the scenarios are in `bin/test-e2e`.

## What is verified

Each cell is asserted against the fake GitHub (`gh.log`, `posted/`), the origin
refs, `STATE_DIR/notes` and `STATE_DIR/held`.

| Workflow | Cases | Ceilings | Class |
| --- | --- | --- | --- |
| `fix-issue`, a fix | branch pushed, pull request created with `Closes #7` | legacy, observe, propose, act, auto | 1, 2, 3 |
| `fix-issue`, a decline | the agent's answer commented, or held, or noted | same | 1, 2, 3 |
| `fix-issue`, a half-done fix | run fails (ABANDONED), nothing pushed, answer comment obeys the gate | legacy, observe, propose, act | 1 |
| `fix-issue`, a claim (`bin/held` 3) | no push, no pull request | legacy, act | 1 |
| classification | a bad or missing answer is level 3, nothing pushed at `auto`; the sensitive-word floor raises 1 to 3; item text is never executed | auto, act | 1 |
| `review-pr`, merge verdict | comment, merge: commented and merged (legacy, auto), commented not merged (act), held comment and no merge (propose), notes only (observe) | all five | n/a |
| `review-pr`, a fix round | `address` runs, the fix reaches the branch (legacy, act, auto), is held (propose) or noted (observe); two review rounds | all five | n/a |
| `review-pr`, merge scope | a stranger's pull request: merged at legacy, refused at act and auto, merged with `GATE_MERGE_SCOPE=all`; the dot logged in as another account: refused | legacy, act, auto | n/a |
| `review-pr`, breaker | a tripped breaker lowers `auto` to `act` | auto | n/a |
| `review-pr`, bad input | `autonomy=` is legacy and never calls the gate; `autonomy=yolo` is rejected by awf | | n/a |
| `publish-held` | a propose record of `fix-issue` (push) and of `review-pr` (comment, push) is released: origin, `gh`, `held/released/`, `released.log`; a second release and a claim are refused | propose | 1 |
| legacy baseline | four runs compared with the workflows of `0a28bc8~1`, the commit before the gate (see below) | legacy | n/a |

Class 1, 2 and 3 apply to `fix-issue` only: it is the one workflow that sets
`ITEM_CLASS`. `review-pr` takes the class of the issue behind a `fix/<N>-*` branch (see D1).

### Legacy equals the pre-gate behaviour

`git archive 0a28bc8~1 workflows` gives the workflows as they were before the
gate was wired in. The same four scenarios (fix with a pull request, decline,
review and merge, review with a fix round) run with those files and with the
current files and no `autonomy` input, with the same seed and fakes. The test
compares, byte for byte after folding the run noise (scratch paths, `fix/N-<timestamp>`,
shas): the ordered `gh` call log, the exit code, every file handed to `gh` (pull
request body, comments), the files on every origin branch, the list of state
files, the agent steps that ran (so no classification call), and that no note or
held record exists. If the commit is not in the clone (shallow checkout) the
section is skipped with a message.

## Upstream (ADR 0043)

Section `upstream` runs `file-upstream` and the `tend-repo` hook with the fake `gh` (`issue list`,
`issue create` and canned upstream issues `up-issues-<owner_repo>.json`, `up-issue-<owner_repo>-<N>.json`)
and the stub agent (`draft-upstream`, `learn`, `scout`). Cases: filed at `auto` (footer, marker,
evidence, create only), a drafting agent, a missing draft, refused at `act`, held at `propose` and
released, note at `observe`, duplicate by title and by error string (open and closed), unreadable
search, weekly and daily cap, no evidence, unlisted repository, the repository itself, no ceiling,
the off switch after two closures (`bin/upstream-check`), the tend hook (listed, unlisted, below
`auto`), and the legacy baseline: `tend-repo` before ADR 0043 (`UPSTREAM_BASE_REV`) against the current
one with no `upstream` input, compared on calls, bodies, state and agent steps.

## Defects found by the harness, now fixed

The first run of this harness found three defects, kept as expected failures
(`XFAIL`) until they were fixed. They are now ordinary assertions, and the
`known_defect` mark is gone from `test/e2e/lib.sh`.

**D1. A pull request born from a class 2 item was merged by `review-pr` at
`auto`.** The class was computed in `fix-issue` and passed only to its own gates;
`review-pr` called `publish-gate merge` with no `ITEM_CLASS`. Now `review-pr`
(`post`, `push`, `automerge`) reads `fix/<repo>/issue-<N>/item_class` for a head
branch `fix/<N>-*` and exports it to every gate call; a missing or invalid file is
3 when a ceiling is set. Other branches and the legacy run are unchanged. Cases
(section `review`): "chain class 2" (not merged, review commented), class 1
(merged), class 3, a missing and an invalid file (notes, no merge, also at `act`),
legacy with no file (merged, no gate call), `fix/<name>` and `fix/7x-*` (no class).
`seed_pr` writes the class file a real `fix-issue` run leaves (`SEED_CLASS`,
default 1, `none` for no file).

**D2. After `propose` and a release, `fix-issue` had pushed a branch and opened no
pull request.** The push gate returned 3 and the step exited before the `open-pr`
gate call. Now a held push also writes the `open-pr` record in the same step
(artifact `fix.md`, `branch`, `base`, `title`, `closes`), and `publish-held`
refuses an `open-pr` whose branch is not on the remote. Cases (section `held`):
two records after `propose`; the pull request first is refused with the message and
leaves the record; push then open-pr opens one pull request with `Closes #7`; no
open-pr record when a claim holds the branch or at `act`.

**D3. A held record's artifact lived in a directory the next run wipes.** Now the
gate copies it under `held/artifacts/<record name>/` (or `notes/artifacts/`),
`artifact:` names the copy and `source:` the original. Cases: a push held, then a
second `fix-issue` run on the same issue, then the release: the held change
reaches the branch; a held answer survives the removal of `STATE_DIR/fix`; the
record lines. Gate unit cases are in `bin/test-publish-gate`, the open-pr refusal
in `bin/test-publish-held`, the dashboard path in `cmd/dashboard/autonomy_test.go`.

## Not faked, not covered

- The model: the stub writes canned files, so no prompt quality, no real tool
  use, no agent that ignores its write scope. The prompts are only checked for
  the line that identifies the step.
- GitHub itself: `gh pr merge` is logged, not executed, so branch protection,
  required checks and the real `--delete-branch` are untested. `pr checks
  --watch` is answered with an empty list ("no check to wait for"), and a pending
  or failing check is not simulated. Forks (`gh repo fork`, the external path of
  `fix-issue`) are not exercised: the fake fails the fork call.
- `awf serve` and `awf-gh` (the dispatchers); runs use `awf run`, which the
  project avoids in production only because it leaves no history.
- `reply-pr`, `fix-ci` and `tend-repo` (gated too, but their set-up needs more
  canned state: review threads, check runs, a findings file). The gate and the
  release are shared code, so only the wiring of those three is unproven here.
- The Discord webhook (the fake `curl` would fail it, and it is unset), the
  dashboard's Release button, the real `bin/held` and its database, and the
  lock waits and CI waits (`CI_WAIT_SECONDS=2`, no pending check).
- The `docker` mode, the container user and the mounted paths.
