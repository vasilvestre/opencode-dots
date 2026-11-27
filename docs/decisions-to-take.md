# Decisions to take

State on 2026-11-27. Each record stays `proposed` until the owner says so. A first step is built for
0034 to 0043 except 0035 (a framing) and 0036's payer; what is left below is only what the code cannot
decide.

## Already decided and accepted

- **0034** (identity): no default mark; marking is opt-in or opt-out per repository or profile.
- **0039** (sources): advisories first; an unmapped Sentry project is diagnosis-only.
- **0040** (autonomy): act and auto first, then observe, then propose; `auto` merges the dot's own pull
  requests; the circuit breaker is optional.

These three are accepted on `main` (commit 9544b6f).

## Decided in direction, not yet accepted

| Record | What the owner said | What is built | Left to accept |
| --- | --- | --- | --- |
| [0036](../ADR/0036-model-budgets-and-caps.md) | alone, the provider quota is the limit; in a team, autonomous fixes need a company cap on the company profile | opt-in `budget: {daily_usd}` per repository or profile, `bin/budget`, enforcement in `bin/tend`, the gate and three workflows | payer, per-person cap, monthly view |
| [0043](../ADR/0043-dot-files-issues-on-named-upstream-repositories.md) | `auto` is the minimum ceiling to file upstream | opt-in `upstream` key, `file-upstream`, the `file-issue` gate action, `bin/upstream-check`, release of held records | the mandatory "automated agent" line (recommended: yes), the first upstream repositories and their weekly caps |

## Open: the owner has not answered

| # | Record | Question | Recommended | Built so far |
| --- | --- | --- | --- | --- |
| 1 | [0037](../ADR/0037-dashboard-users-and-roles.md) | Is the `Tailscale-User-Login` header an acceptable source of identity? | yes | append-only audit of writes, no roles |
| 2 | 0037 | Does a viewer role need to exist before the first teammate? | no, audit first | |
| 3 | [0038](../ADR/0038-what-two-dots-share.md) | Is one acting dot per repository, named in `repos.yaml`, enough for now? | yes | `settings.dot_id` and the `dot` key; foreign repositories dropped from generated files |
| 4 | 0038 | May shared lessons wait? | yes | |
| 5 | [0041](../ADR/0041-linking-work-items-is-a-proposal.md) | Is `propose` with a person's confirmation the only mode for now? | yes | proposals, accept, decline, split, the `_propose-links` workflow |
| 6 | 0041 | Across a trust boundary: references only, or refuse? | refuse for now (built that way) | |
| 7 | [0042](../ADR/0042-workstations-are-workers-of-an-instance.md) | First version: workers are only listed, or they pull tasks? | listed only | `worker` table, `bin/repos db worker`, a Workers card |
| 8 | 0042 | Worker authentication: an instance token, or tailnet identity? | tailnet identity | |
| 9 | [0035](../ADR/0035-a-dot-is-an-engine-instance-with-a-policy-profile.md) | Do you accept "a dot is an engine instance with a policy profile" as the framing? | yes | profiles carry autonomy, identity, budget and dot |

## Not decisions: things only the machine's owner can do

- **Enable `identity: marker` on this repository.** The prepared file is `state/prep/repos.yaml.identity-marker`:
  `cp state/prep/repos.yaml.identity-marker repos.yaml && make repos`.
- **Install the timers** for the breaker, the upstream check, advisories, health and propose-links. The commands are
  in `docs/breaker.md`, `docs/health.md`, `docs/advisories.md` and `docs/links.md`.
- **Name the first upstream repositories** and their weekly caps, then list them under `upstream:` in `repos.yaml`.
- **Accept a record** once its answers are in: `bin/adr accept N`.

## Not built, and why

- Roles and a viewer (0037), the claim channel and shared lessons (0038), task pulling and worker
  authentication (0042), automatic linking (0041): each changes what the dot may do, so each waits for its answer above.
- Upgrade pull requests for advisories: they need `composer update` run in a sandbox with network, which is a
  security decision for the owner.
