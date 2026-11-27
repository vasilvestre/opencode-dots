# 0040. Each item is classified, each repository has an autonomy ceiling, the action is the lower of the two

- Status: accepted
- Date: 2026-11-26
- Supersedes: none
- Superseded by: none

## Context

Two questions got mixed. One is what an item deserves: a typo and an authentication bug are not
the same work. The other is what a dot may do on a repository: a personal package and a client
project are not the same risk. An existing agent that triages tickets answers the first with
three levels, decided after analysis, never before: a complete fix, a fix with reinforced
review, or a human diagnosis. Its own notes say the lowest level is a floor, not a guarantee,
because an item can still be under-rated.

[0032](0032-trust-levels-per-repository.md) answers a third question, what may leave and where,
and is unrelated to both. Today autonomy is only a few switches: `auto_merge`, `fix_issue`,
the delegation of [0015](0015-dots-take-delegation.md). See use cases A4, B3, D1.

## Decision

We keep three separate settings and combine two of them.

**Classification, per item.** The analysis step sets a level with its reason, a confidence and
what is missing, and may reclassify when the item changes, keeping the history:

1. complete fix: the cause is located and the change is local;
2. fix with reinforced review: a fix is possible but crosses several areas;
3. human diagnosis: too complex or too sensitive; the dot only writes an analysis.

A sensitive vocabulary can force level 3 as a floor.

**Autonomy ceiling, per repository** (`autonomy`, lowered but never raised per workflow):

- `observe`: it analyses and writes an internal note. No patch, no pull request.
- `propose`: it prepares the patch or comment and holds it in the dashboard until a person
  publishes it.
- `act`: it opens pull requests and comments itself. It never merges.
- `auto`: it owns the **code** of the repository: it picks the code work worth doing, keeps
  dependencies current, reviews and merges when the review says so and the checks pass.

`auto` stops at the code. Deploying is CI/CD, publishing a release belongs with it, and the
backlog and priorities are project management. None of these is the dot's.

**Effective action = the lower of the class outcome and the ceiling.** Level 3 gives an analysis
whatever the ceiling. Level 2 gives a pull request with reinforced review and is never merged by
the dot. Level 1 gives a pull request, and a merge only at `auto`.

`trust` bounds the ceiling: `client` and `strict` refuse `auto`, as they already refuse
`auto_merge`.

### Settled in discussion (2026-11-26)

- **Order of delivery.** First the `autonomy` key with `act` and `auto`, derived from today's
  switches and enforced at the one shell step that publishes ([0002](0002-shell-steps-own-side-effects.md)),
  so no configuration changes behaviour. Then `observe`. `propose` last, because it needs a held
  output, a button and a `publish` workflow. The dashboard holds no GitHub token: a click files an
  approval and a workflow publishes.
- **What `auto` merges.** By default the dot's own pull requests and those of dependency bots
  (`own`). Merging pull requests written by people (`all`) is a separate choice, made per
  repository. In every case `auto` honours claims ([0026](0026-claims-and-presence.md)): it never
  pushes to or merges a branch a human holds.
- **Circuit breaker, optional.** When enabled for a repository, a revert of a merge made by the dot,
  or a red check on the default branch after such a merge, drops the repository to `act` and the
  dashboard says why. Raising it again is a human act. Stopping on given subjects is the same
  mechanism with a narrower trigger. It is off unless asked for.
- **Level 2, "reinforced review"**, is proposed and not yet agreed: never merged by the dot, a second
  review pass with another model, a `needs-human-review` label, and the checks green before the pull
  request opens.

## Consequences

Easier: the personal profile can set `auto` on its own repositories and the company profile
`act`, with the same workflows. Every action carries its class, ceiling and reason, which is the
trace a company asks for.

Harder: the classification prompt becomes a part to test and measure, because an under-rated item
is a real risk. Existing switches (`auto_merge`, `fix_issue`) must map onto the ceiling without
breaking current configuration.

## Alternatives

One scale mixing both: a repository would need a different rule per kind of item. Autonomy
decided only per workflow: loses the single statement "this repository is fully mine".
