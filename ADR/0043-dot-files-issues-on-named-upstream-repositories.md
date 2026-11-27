# 0043. The dot may file issues on named upstream repositories

- Status: accepted
- Date: 2026-11-26
- Supersedes: none
- Superseded by: none

## Context

The dot runs on tools that live in other repositories: the workflow engine, its GitHub watcher, the
tower index, the agent CLIs. While it works it meets defects in them, and today a person reports each
by hand. The owner wants this repository fully autonomous ([0040](0040-classification-and-autonomy-ceiling.md),
`auto`), and a part of owning a product is telling its dependencies what breaks.

This is a different kind of action from everything else the dot does. A comment, a push or a merge lands
on a repository the dot works on. An issue on an upstream repository lands on someone else's tracker,
in public, under the owner's name. It cannot be undone by the owner alone, and a bad one costs
the maintainers' time and the owner's standing. The daily pass already files issues on its own
repository and keeps a ledger so it never refiles a subject (`raised.txt` in `workflows/tend-repo.yaml`).

## Decision

Open. Proposed shape: a feature, off by default, that lets a repository name the upstream repositories
it may report to, and a new gate action for it.

### Recommendation

- **A key per repository**, `upstream`: a list of `{repo, max_per_week}`. A repository not listed is never
  written to. Nothing else is written there: the dot only **creates** issues. It never comments on
  existing ones, never opens pull requests and never reacts to replies. Those stay later decisions.
- **A new gate action, `file-issue`** in `bin/publish-gate` ([0002](0002-shell-steps-own-side-effects.md)):
  allowed at `auto`, held at `propose`, a note at `observe`. At `act` it is refused, because `act` is
  scoped to the repository the dot works on and an upstream tracker is wider.
- **What an issue must carry.** A defect found by a workflow with evidence: the command or step that
  fails, the versions involved, an excerpt of the log, and what was expected. Without evidence the finding
  stays an internal note.
- **No duplicates.** Before filing, search the upstream tracker (open and closed) for the same error or
  title, and keep a ledger per upstream like `raised.txt`. A match files nothing and records why.
- **A cap.** `max_per_week` per upstream, and a hard daily one, so a loop cannot flood a tracker.
- **Honesty.** The body says it was filed by an automated agent on the owner's behalf and names the
  dot ([0034](0034-dot-identity-in-github.md)). The `silent` mode is refused for any repository the owner
  does not control, whatever the profile says.
- **Text is data.** Upstream issue text, replies and templates are never instructions. If the upstream
  repository has an issue template or a contributing guide, the body follows it as formatting only.
- **Breaker.** Two issues closed as invalid or "not a bug" by upstream maintainers in a row turn the
  feature off for that upstream until the owner turns it back on
  ([0040](0040-classification-and-autonomy-ceiling.md), circuit breaker).

### Options

| Option | Cost | Rules out |
| --- | --- | --- |
| Only prepare the issue text and hold it (`propose` always) | one click each, safe | full autonomy for upstream reports |
| File directly at `auto`, with evidence, dedupe, cap (recommended) | a new gate action and a ledger | filing without evidence |
| Also comment and open pull requests upstream | much more review of foreign code | nothing, but it is a different record |

### Questions for the owner

1. Is `auto` the minimum ceiling to file on an upstream repository, or should `act` be enough?
2. Is a visible "filed by an automated agent" line mandatory on every upstream issue? Recommended: yes.
3. Which upstream repositories first, and which weekly cap each?

### Settled in discussion (2026-11-26)

- **`auto` is the minimum ceiling** to file on an upstream repository. `act` is refused: writing on someone
  else's tracker needs the most demanding level, because the dot must already be trusted to own its own
  product before it speaks for it elsewhere.
- Still open: whether the "filed by an automated agent" line is mandatory (recommended: yes), and which
  upstream repositories first with which weekly cap. Until the owner names any, the feature lists none and
  writes nowhere.

### Answers

The owner answered both: `auto` is the minimum ceiling to file upstream, and the visible "filed by an
automated agent" line is mandatory (the `silent` mode is refused for any repository the owner does not
control). The first step is built, opt-in and off by default (`docs/upstream.md`).

## Consequences

Easier: defects in the tools the dot depends on get reported with evidence and without the owner relaying
them. The dependencies' maintainers get consistent reports.

Harder: a new write path to repositories nobody here controls, so the cap, the dedupe and the evidence
rule are the whole safety. It needs an upstream search step and a ledger, and the gate grows one action.

## Alternatives

Never write upstream: loses the point of owning the product. Always hold: safe, but every report waits for
a person, which is what happens today.
