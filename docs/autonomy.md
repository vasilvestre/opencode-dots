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
| `auto` | allow | allow, except class 2 (refused) |

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
so the checkout can be removed without losing the work.

## Where it is wired

| Workflow | Gated |
| --- | --- |
| `review-pr` | `post` (comment), `push`, `automerge` (merge) |
| `reply-pr` | `push`, `post` (comment) |
| `fix-ci` | `push` |
| `fix-issue` | `push`, `open-pr`, the issue comment in `push` and `notify` |
| `tend-repo` | `file` (issue, as comment), `patch` (push, open-pr), merge; the nested `review-pr` run receives the same ceiling |

The hidden `_fix-issue-*` workflows publish nothing, so they carry no gate.

`ITEM_CLASS` is read from the environment; no workflow sets it yet. The
classification step will provide it.
