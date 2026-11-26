# End-to-end tests

`bin/test-e2e` runs the real workflow files (`workflows/*.yaml`) through the real
`awf`, offline. The other tests extract one shell step or run the gate alone;
this one runs a whole state machine, including `call_workflow`, the agent steps
and the publishing steps, and then asks what reached "GitHub".

```bash
bin/test-e2e                  # everything, about a minute
bin/test-e2e fix-issue        # one section: fix-issue classify review held baseline
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

`test/e2e/lib.sh` holds the helpers (`case_new`, `seed_pr`, `run_wf`, `check`,
`known_defect`); the scenarios are in `bin/test-e2e`.

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
`ITEM_CLASS`. `review-pr` has no class (see D1).

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

## Known defects (expected failures)

A defect is written as the check that should hold. While it fails it prints
`XFAIL ... [Dn known defect]` and does not fail the run. When it starts to pass
it prints `XPASS` and fails the run, so the mark is removed with the fix. The
workflows were not edited to hide any of these.

**D1. A pull request born from a class 2 item is merged by `review-pr` at `auto`.**
`docs/autonomy.md`: "Class 2 is never merged by the dot", and the table refuses a
class 2 merge at `auto`. The class is computed in `fix-issue` (`level` writes
`fix/<repo>/issue-<n>/item_class`) and is passed only to the gates of `fix-issue`.
`review-pr` `automerge` calls `publish-gate merge` with `GATE_PR_AUTHOR` and no
`ITEM_CLASS`, so the gate sees "not classified" and only the ceiling counts. The
pull request `fix-issue` opened is a `fix/N-*` branch with `Closes #N`, exactly
what `automerge` takes. Reproduction: `fix-issue` with `autonomy=auto` on an issue
classified 2 (pull request opened), then `review-pr` with `autonomy=auto` on that
pull request with a `merge` verdict: `gh pr merge` is issued. Expected: refused.
The e2e case is "chain class 2" in the `review` section. (A class 3 item opens no
pull request, so it is not reachable this way.)

**D2. After `propose` and a release, `fix-issue` has pushed a branch but opened no
pull request, and nothing holds the pull request text.** In `fix-issue`
`push`, the gate call for `push` returns 3 and the step `exit 0`s before it reaches
the `open-pr` gate call, so only a `push` record exists. `publish-held` of that
record pushes the branch and stops; no `open-pr` record exists to release, and
`fix.md` (title and body with `Closes #N`) is not referenced by any record. The
person must write the pull request by hand. Reproduction: `fix-issue` at `propose`
on a fixable issue, look in `held/`: one `push` record (and the held answer
comment), no `open-pr`. Expected: an `open-pr` record, or the push release also
opens the pull request. E2E: "release push: the pull request text is held too".

**D3. A held record's artifact lives in a directory the next run wipes.** The
record names `fix/<repo>/issue-<n>/held.patch` (and `answer.md` for a comment).
`fix-issue` `fetch` does `rm -rf "$d"` at the start of every run for that issue
(`review-pr` `fetch` does the same for `reviews/pr<N>-<sha>`). A second run (the
issue labelled again, a retry) deletes or overwrites the artifact; releasing the
first record then fails with "the artifact is missing or empty", or, if the second
run wrote a new `held.patch`, applies the second run's patch under the first
record's branch name. Reproduction: `fix-issue` at `propose` (patch), then
`fix-issue` again at `propose` where the agent only declines, then `publish-held`
with the first push record: the run fails. Expected: the record keeps its own copy
of the artifact (under `held/`), as the gate's doc says a held item can be
released "once, when a person asks". E2E: "a held push survives the next
fix-issue run".

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
