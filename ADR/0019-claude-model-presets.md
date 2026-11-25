# 0019. Model presets are per provider

- Status: accepted
- Date: 2026-11-25
- Supersedes: 0018
- Superseded by: none

## Context

ADR 0018 holds two global presets, `light` and `default`, and projects them into
workflow input defaults. Every ID is an opencode `provider/model`. `review-pr`
can now run on the `claude` CLI for one repository (`provider: claude` in
`repos[].inputs`), and awf passes the model string to that CLI unchanged. The
claude CLI does not know `mistral/deepseek-v4-1-flash`; it takes `sonnet`,
`haiku` or `claude-sonnet-4-5`. A repository routed to claude therefore ran with
a model its CLI cannot resolve. The shared model pattern also requires a `/`, so
a claude name could not be stored at all.

awf interpolation has no conditional, so a workflow cannot pick a model from the
provider by itself.

## Decision

We keep one preset file and add a second pair for claude. `model-presets.yaml`
stays at version 1 and gains two optional preset keys, `claude_default` and
`claude_light`. A file without them stays valid and gets `sonnet` and `haiku`.
Two new roles, `review-pr/claude_review_model` and
`review-pr/claude_address_model`, take them. Claude names use their own pattern
(`ClaudePattern`: an alias or a full name, no provider prefix, an optional
`[1m]` suffix), and a Claude name is refused in an opencode preset and the other
way round.

`review-pr` gets two shell steps, `pick_review_model` and `pick_address_model`.
Each prints the opencode or the claude model according to `inputs.provider`, and
the `review` and `address` agent steps read that output. The dashboard checks
that each pick step reads its three inputs. The effective review and address
models must differ for each provider.

The defaults avoid the most expensive model: `sonnet` reviews and `haiku` fixes.
An operator who has the quota sets a stronger one in the Config view.

## Consequences

- A repository routed to `provider: claude` gets claude names, and the others
  keep opencode IDs. Both sets are edited in the same Config form.
- Only `review-pr` exposes `provider`, so only its two roles have a claude twin.
  Routing another workflow to claude means a new role pair and a new pick step.
- Eager projection still applies: the claude presets are written into the
  workflow defaults, and drift detection covers them.
- A pick step adds two near-instant steps to each run and hides them in the
  dashboard trail.
- A Run dialog override of `review_model` or `address_model` is ignored when the
  provider is claude, and the other way round.

## Alternatives

- **Per-repository model inputs in `awf-gh.yaml`.** No code, but each routed
  repository repeats its models by hand, and the dashboard cannot edit them.
- **A guard step that refuses a mismatched model.** It turns a confusing failure
  into a clear one but does not fix the model.
- **One pattern accepting both syntaxes.** It would let an opencode ID reach the
  claude CLI without any error until the run.
