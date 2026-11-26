# Decisions to take

## Answered (2026-11-26)

- **0034**: `signed` is not the default everywhere; marking is opt-in or opt-out per repository or profile.
- **0039**: advisories are wired end to end first; an unmapped Sentry project is diagnosis-only.
- **0036**: alone, the provider quota is the limit; in a team, autonomous fixes need a company cap on the
  company profile, developer-driven ones use the developer's own quota.

One pass. Each line is a question, the record it unblocks and the recommended answer. Ordered by
what blocks most. Every record stays `proposed` until you say so; the recommendation in each is the
smallest step that can ship.

## 1. Identity: blocks 0038 and the team cases

| Q | Record | Question | Recommended |
| --- | --- | --- | --- |
| 1 | [0034](../ADR/0034-dot-identity-in-github.md) | Is `signed` (footer plus invisible marker) the default everywhere? | yes |
| 2 | 0034 | Is a signed footer acceptable on open source repositories that did not ask? | yes |
| 3 | 0034 | May `app` and `account` wait until a team member asks? | yes |

## 2. Sources: blocks 0041 and the classification work

| Q | Record | Question | Recommended |
| --- | --- | --- | --- |
| 4 | [0039](../ADR/0039-work-sources-are-adapters.md) | Is advisory the first source wired end to end? | yes |
| 5 | 0039 | An unmapped Sentry project: diagnosis-only or ignored? | diagnosis-only, like Jira |
| 6 | 0039 | Is the project-to-repository mapping kept in `repos.yaml`? | yes |
| 7 | [0041](../ADR/0041-linking-work-items-is-a-proposal.md) | Is `propose` with a person's confirmation the only mode for now? | yes |
| 8 | 0041 | Wait until two sources are wired before building it? | yes |
| 9 | 0041 | Different trust sides: store references only, or refuse the link? | references only |

## 3. Spending: blocks sharing a dot

| Q | Record | Question | Recommended |
| --- | --- | --- | --- |
| 10 | [0036](../ADR/0036-model-budgets-and-caps.md) | First unit: daily cap per repository, or one cap for the whole dot? | per repository |
| 11 | 0036 | At the cap: note only, or note and a comment on the pull request? | note only |
| 12 | 0036 | May the payer wait until the first shared dot? | yes |

## 4. Two dots and workstations: blocks teams

| Q | Record | Question | Recommended |
| --- | --- | --- | --- |
| 13 | [0038](../ADR/0038-what-two-dots-share.md) | Is one acting dot per repository, named in `repos.yaml`, enough for now? | yes |
| 14 | 0038 | Does the server need its own id to compare against, or reuse the hostname? | its own id |
| 15 | 0038 | May shared lessons wait? | yes |
| 16 | [0042](../ADR/0042-workstations-are-workers-of-an-instance.md) | First version: workers are only listed, or they pull tasks? | listed only |
| 17 | 0042 | Worker authentication: an instance token, or tailnet identity? | tailnet identity |
| 18 | 0042 | May a worker's cost stay invisible until 0036 is decided? | yes |

## 5. Dashboard access: blocks a manager or client view

| Q | Record | Question | Recommended |
| --- | --- | --- | --- |
| 19 | [0037](../ADR/0037-dashboard-users-and-roles.md) | Is the `Tailscale-User-Login` header an acceptable source of identity? | yes |
| 20 | 0037 | Is an audit of writes only (no reads) enough for now? | yes |
| 21 | 0037 | Does a viewer role need to exist before the first teammate? | no, audit first |

## What the built code already settles

- 0038: claims let a bot never veto a bot, so two dots would collide today. Claims alone are not
  the answer; the `dot` key is.
- 0039: Jira has the project mapping and a diagnosis-only flag; Sentry and advisory do not. Make
  the Jira shape the contract.
- 0034: seven comment sites and no shared emission point. One script before any config key.
- 0041: `work_item_ref.ref` is UNIQUE, so a link must be a proposal that moves rows, not a second
  reference.
