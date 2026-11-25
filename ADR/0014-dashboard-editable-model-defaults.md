# 0014. The dashboard edits workflow model defaults

- Status: accepted
- Date: 2026-11-24
- Supersedes: none
- Superseded by: 0018

## Context

Every agent step pins an OpenCode model in its workflow YAML. Automatic runs from `awf-gh`, Ask and `bin/tend` do not pass model inputs. Changing a model therefore needs a file edit today. The dashboard needs persistent defaults for those runs. It also needs a one-run override in the existing Run dialog. OpenCode identifies a model as `provider/model`, with an optional `#variant`. The `provider: opencode` field in awf selects the adapter, not the model provider.

## Decision

We store model defaults in native awf workflow inputs and reference them from each agent step's `options.model`. Single-agent workflows use `model`. `review-pr` uses `review_model` and `address_model` so the reviewer keeps a different model from the fixer. The Config view edits these defaults through `GET /api/models` and `PUT /api/models`. The API changes only named `default` scalars, preserves all other YAML bytes, validates model identifiers, and reuses the dashboard's trusted request checks, backups and atomic writes. The existing Run dialog overrides an input for one run. Automatic dispatch omits model inputs and receives the saved defaults.

## Consequences

A saved default applies to the next run without restarting awf or OpenCode. A running request keeps the model it started with. Dashboard-started runs retain exact inputs in browser `localStorage`, including a model override. Automatic runs have no such browser record, so replay may use the current saved defaults instead of the historical model. The dashboard validates identifier syntax, not provider availability. An unavailable model fails the next agent step. The API can edit only workflows that follow the model-input convention. The generic YAML editor can still break that convention, so the form reports conflicts instead of overwriting them. Updating several workflow files is not one filesystem transaction. A run that starts during a save can use mixed defaults.

## Alternatives

- Environment variables: awf reads its environment at startup, so changing a model would need a service restart.
- Per-run inputs only: automatic runs would keep stale defaults.
- A separate defaults file: it would duplicate state and need a new synchronization path.
- Rewrite whole YAML files: marshalling would discard comments and formatting that make workflows readable.
