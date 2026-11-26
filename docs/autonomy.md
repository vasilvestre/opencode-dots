# Autonomy ceiling at the publishing step

ADR 0040 gives each repository a ceiling (`autonomy`) and each item a class. The
effective action is the lower of the two. `bin/publish-gate` is the one place that
decides, and only shell steps call it (ADR 0002): the agent writes files, never
publishes, and has no way to skip the gate.

## The rules

| Ceiling | comment, push, open-pr | merge |
| --- | --- | --- |
| empty (legacy) | allow | allow |
| `observe` | note only | note only |
| `propose` | hold | refused |
| `act` | allow | refused |
| `auto` | allow | allow for the dot's own and dependency-bot pull requests, except class 2 (refused) |

### The action `file-issue` (ADR 0043)

`bin/publish-gate file-issue` is for creating an issue on an upstream tracker (`docs/upstream.md`).
It is wider than the others, so only `auto` allows it: `propose` holds it (the record also has a
`target:` line, from `GATE_TARGET`), `observe` writes a note, `act` is refused (exit 5, "writing on
an upstream tracker needs auto"). An empty ceiling stays the legacy allow, but the only caller,
`workflows/file-upstream.yaml`, never runs without a ceiling.

### Merge scope

At `auto`, a merge is allowed by default only for a pull request the dot wrote or
a dependency bot wrote (`dependabot`, `renovate`, `github-actions`, with or
without `[bot]` or `app/`). `GATE_PR_AUTHOR` is the author, `DOT_LOGIN` the dot's
login (default: the login `gh` is signed in as). Anyone else's pull request, or
an unknown author, is refused (exit 5) and the reason names the author. The
opt-in `GATE_MERGE_SCOPE=all` (read from the run environment by `review-pr` and
`tend-repo`) widens it to every author; class 2 and the ceiling still apply.
There is no per-repository key for it yet.

The dot acts under the owner's login, so "wrote by the dot" and "wrote by the owner"
share one author. When the repository marks what the dot writes (`identity: marker`
or `signed`, `docs/identity.md`), the caller passes `GATE_IDENTITY` and
`GATE_PR_BODY_MARKED=1` when the pull request body carries the dot marker. A pull
request by `DOT_LOGIN` without the marker is then a human's: the merge is refused
(exit 5) and the reason names the missing marker. With `identity` off or empty
nothing changes. A dependency bot needs no marker, and `GATE_MERGE_SCOPE=all` skips
the check along with the rest of the scope.

Class 3 (human diagnosis) yields a note only for every action at every non-empty
ceiling. Class 2 is never merged by the dot. An empty `ITEM_CLASS` means "not
classified": only the ceiling counts. An empty `AUTONOMY` is the legacy case and
allows everything, exactly as before the gate existed.

## Exit codes

| Code | Meaning | Side effect |
| --- | --- | --- |
| 0 | allow | none |
| 3 | hold (propose) | a file under `$STATE_DIR/held/` names the artifact |
| 4 | note only (observe, class 3) | a file under `$STATE_DIR/notes/` |
| 5 | refused (merge below `auto`, class 2 merge, unknown value or action) | none |

The reason is always printed on stderr. A bad `AUTONOMY`, `ITEM_CLASS` or action
is refused (5), never allowed.

## Environment

`AUTONOMY`, `ITEM_CLASS` (optional), `STATE_DIR` (default `/state`), `GATE_REF`
(what is published, for the trace), `GATE_ARTIFACT` (the prepared comment or patch).
`GATE_REPO` (`owner/repo`) is exported at every call so the circuit breaker finds its
file. `GATE_PR_AUTHOR`, `DOT_LOGIN`, `GATE_MERGE_SCOPE` decide the merge scope, with
`GATE_IDENTITY` and `GATE_PR_BODY_MARKED` when the repository marks the dot's work.
`GATE_REPO_SOURCE` and `GATE_UPSTREAM` (file-issue only) end up in a held record as
`repo:` and `upstream:`.
`GATE_BRANCH`, `GATE_BASE`, `GATE_PUSH_REPO`, `GATE_TITLE`, `GATE_CLOSES` are optional
and only end up in a held record (`docs/publish-held.md`), one line each, control
characters dropped, a line omitted when unset. Every workflow call passes the ones it
knows at that point. With `autonomy` empty the gate is not called, so none of this
exists.

## How a step calls it

A workflow declares an optional `autonomy` input (default empty, pattern
`^(observe|propose|act|auto)?$`) and puts the call before the publication. When the
input is empty the script is not even called, so the step is what it was:

```bash
if [ -n "{{.inputs.autonomy}}" ]; then
  grc=0
  AUTONOMY="{{.inputs.autonomy}}" GATE_REF="$repo#$iss" GATE_ARTIFACT="$d/fix.md" \
    "${DOTS_HOME:-/work}/bin/publish-gate" push || grc=$?
  [ "$grc" = 0 ] || { echo "autonomy: not pushed (gate $grc)"; exit 0; }
fi
```

A declined action exits 0 (a guard is not an error). A missing script makes the
call fail, which counts as refused: the gate fails closed when a ceiling is set.
Before a push is held, the step writes the patch next to the run (`held.patch`)
so the checkout can be removed without losing the work. That file lives in a
directory the next run wipes, so the gate copies the artifact of every held or
note record to `held/artifacts/<record name>/` (`notes/artifacts/...`), points
`artifact:` at the copy and keeps the original on a `source:` line (cap
`GATE_ARTIFACT_CAP`, default 5 MiB; see `docs/publish-held.md`). The dashboard
resolves the copy like any path under `STATE_DIR`.

At `propose`, when the push of `fix-issue` is held the same step also writes the
`open-pr` record, so a person can release the push and then the pull request.

## Where it is wired

| Workflow | Gated |
| --- | --- |
| `review-pr` | `post` (comment), `push`, `automerge` (merge) |
| `reply-pr` | `push`, `post` (comment) |
| `fix-ci` | `push` |
| `fix-issue` | `push`, `open-pr`, the issue comment in `push` and `notify` |
| `file-upstream` | `file` (`file-issue`, on an upstream repository) |
| `tend-repo` | `file` (issue, as comment), `patch` (push, open-pr), merge; the nested `review-pr` run receives the same ceiling |

The daily pass gets its ceiling from `tend.list`: a repository that sets `autonomy` has
`autonomy=<level>` on its line and `bin/tend` posts it as the input `autonomy`. A repository that only
sets `tend.auto_merge` has no such field, so `tend-repo` sees no ceiling, as before; the derived value
is not turned into one.

The hidden `_fix-issue-*` workflows publish nothing, so they carry no gate.

## Where `ITEM_CLASS` comes from

`fix-issue` sets it. With a ceiling, it classifies the issue before the agent
works (`classify_check`, `classify_text`, `classify`, `level`; see
`docs/classification.md`), writes the level to `fix/<repo>/issue-<n>/item_class`,
and every `bin/publish-gate` call in the run passes it. A missing or invalid
result is level 3: the run still analyses, and the gate turns each publication
into a note. With no ceiling nothing is classified and `ITEM_CLASS` stays unset.

`review-pr` classifies nothing itself, but a pull request that `fix-issue` opened
has a head branch `fix/<N>-*`. With a ceiling set, `review-pr` reads
`fix/<repo>/issue-<N>/item_class` for that branch and passes it as `ITEM_CLASS` to
every gate call of the run (`post`, `push`, `automerge`). So a class 2 pull request
is not merged at `auto`, and a class 3 one only produces notes. If the head is a
`fix/<N>-*` branch and the file is missing or not 1, 2 or 3, the class is 3 (fail
closed). Any other branch, including `fix/` with no number, has no class: only the
ceiling counts. With no ceiling the step does not read the file.

`tend-repo` does not classify: it files its own findings, it does not work from
an outside item. Its gate calls still carry no class, only the ceiling counts.

## Related pieces

- `docs/breaker.md`: the optional circuit breaker that lowers `auto` to `act` after a revert
  or a red default branch.
- `docs/publish-held.md`: the `publish-held` workflow that releases a record held at `propose`.
  `propose` is accepted by the config now that this workflow exists.
- `docs/autonomy-view.md`: the read-only dashboard page for notes and held records.

