# 0052. Workflows read the model presets when a run starts

- Status: accepted
- Date: 2026-11-27
- Supersedes: 0019
- Superseded by: none

## Context

ADR 0018 keeps global model presets in `model-presets.yaml` and projects them, eagerly, into the
`default:` of each workflow model input, because awf reads only workflow defaults. ADR 0019 added
the claude presets on top. The projection is the most fragile code of the dashboard: it finds the
byte span of a YAML scalar to rewrite it without touching a comment, plans a save across several
files, backs each up, rolls back on a failure, and reports drift when someone edits a default by
hand. It is about 2 500 lines with its tests, and a run that starts during a save can see mixed
defaults.

`review-pr` already shows a way out (ADR 0019): a shell step prints the model, and the agent step
reads `{{.states.pick_review_model.Output}}`.

## Decision

We stop writing models into workflows. Each workflow that runs an agent on a preset role starts
with a `pick_model` step that prints the input if the run gave one, and otherwise asks
`bin/model ROLE PRESET`, which reads `model-presets.yaml` when the run starts. The model inputs
default to empty. `review-pr` does the same in its two existing pick steps, once per provider.

The file keeps its shape: `version`, the four presets (`light`, `default`, `claude_light` and
`claude_default`, all required now) and `overrides` by role. The role table lives in
`internal/models`; the workflows state the assignment at the call site, and a test fails when the
two differ. The dashboard reads the file, validates an update (identifier shape, known roles,
distinct reviewer and fixer per provider) and writes it atomically with a `.bak`. It no longer
reads or edits a workflow.

## Consequences

- A saved change applies to the next run with no second file to keep in step: no drift, no
  migration mode, no multi-file rollback. A run in progress keeps the model it started with.
- `internal/models` shrinks from about 1 200 lines to about 250, and the models tests from about
  2 400 to about 270.
- Each workflow gains one near-instant step, hidden in the dashboard trail like the pick steps.
- A machine that runs workflows needs `model-presets.yaml` next to `bin/` (the same tree that
  holds the scripts); without it the pick step fails and the run stops before any agent call.
- `bin/model` reads the file with awk, so the file must keep the shape the dashboard writes.
- A trust level that lists `allow.models` still sees only models set on a rule; the model read at
  run time is not in the generated watcher files.

## Alternatives

- **Keep the eager projection, drop the rare cases** (legacy payload, migration, multi-file
  rollback). It leaves the byte-span surgery and the drift problem, which are the cost.
- **A Go subcommand instead of `bin/model`.** The scripts the workflows call are already
  symlinked into the offline test tree; a binary would not be, and the image would need a third
  program.
