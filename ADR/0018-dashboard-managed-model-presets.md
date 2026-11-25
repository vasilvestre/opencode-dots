# 0018. The dashboard manages global model presets

- Status: superseded by 0019
- Date: 2026-11-25
- Supersedes: 0014
- Superseded by: 0019

## Context

ADR 0014 made each workflow model default independently editable. The six
workflows now expose seven model roles. An operator who wants a light setup must
edit several unrelated scalars. The dashboard needs to express that global
choice without losing per-role exceptions.

Automatic runs from `awf-gh`, Ask and `bin/tend` still pass no model input. The
Run dialog must still override one run. awf resolves each agent step from a
concrete `options.model`; it cannot read our preset file. OpenCode global config
and aliases are not the source: awf passes explicit model IDs, and aliases are
provider-scoped.

## Decision

Track a root `model-presets.yaml` file with version 1. It holds two global
presets, `light` and `default`, plus optional per-role overrides:

```yaml
version: 1
presets:
  light: provider/light-model
  default: provider/default-model
overrides:
  review-pr/review_model: provider/reviewer#thinking
```

The role mapping is fixed in code:

| Role | Preset |
| --- | --- |
| `ask-pr/model` | `light` |
| `fix-ci/model` | `light` |
| `reply-pr/model` | `light` |
| `review-pr/address_model` | `light` |
| `fix-issue/model` | `default` |
| `tend-repo/model` | `default` |
| `review-pr/review_model` | `default` |

The API resolves each role by override first, assigned preset second. It eagerly
projects the result into that workflow input's concrete default. Workflow YAML
remains the runtime source, so awf needs no preset support.

The Config view edits the two preset IDs and one optional override per role.
`GET /api/models` preserves `variables` and `available`. It adds `presets`,
`migration_required`, and each variable's `preset` and `override`.
`PUT /api/models` accepts presets and overrides. A string sets an override;
`null` clears it. The existing `{variables}` request remains a compatibility
path that creates explicit overrides. Only `/api/models` writes
`model-presets.yaml`; the generic editor does not expose it.

Reuse the model-ID validator and length limit. Keep the trusted `Host` and
`Origin`, JSON, size, exact-key, structural-conflict and write-lock checks.
Reject invalid YAML, unknown versions, unknown keys, invalid or empty IDs, and
structural conflicts. Reject a configuration whose effective
`review-pr/review_model` and `address_model` are equal. Report drift when a
workflow default differs from the resolved configuration. Block the next preset
save until the operator reconciles it.

A missing preset file means migration mode. `GET` does not write during
migration. Derive `light` from the current `ask-pr/model` default and `default`
from the current `fix-issue/model` default. Make every differing role an
override. This preserves all current runtime values. The first save creates the
preset file.

Prepare workflow scalar replacements and the serialized preset file as one save
plan. Preserve all other workflow YAML bytes. Write workflow files before the
preset file. Back up each existing changed file. A first-time preset file needs
no backup. An unchanged save creates no backups. If a later write fails, roll
back written workflows and remove a newly created preset file.

## Consequences

- A saved change applies to the next run without restarting awf or OpenCode. A
  running request keeps the model it started with.
- Automatic-run compatibility stays intact because dispatch still omits model
  inputs. Per-run override compatibility stays intact because the Run dialog
  still supplies concrete workflow inputs.
- Replay semantics stay intact. A dashboard-started run keeps exact inputs in
  browser storage. An automatic run without that record may replay with current
  saved defaults.
- The preset file duplicates authoring state with concrete workflow defaults.
  Drift detection keeps manual edits from being silently overwritten.
- A multi-file save is not one filesystem transaction. A run that starts during
  the save can observe mixed defaults.
- Model syntax validation does not prove provider availability. An unavailable
  model fails the next agent step.

## Alternatives

- **Preset-only runtime indirection.** awf cannot consume `model-presets.yaml`.
  Supporting it would need an awf change or a dispatch wrapper. Eager projection
  keeps manual runs and automatic runs on the same concrete inputs.
- **OpenCode global config or aliases as the source.** awf passes explicit model
  IDs, and aliases are provider-scoped. Neither expresses this project's fixed
  workflow-role mapping.
- **Keep seven independent defaults.** That avoids duplicated state but makes a
  global model change a repeated edit with no single expression of intent.
