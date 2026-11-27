# 0034. The dot says who it is in GitHub, and the identity is a per-repository choice

- Status: accepted
- Date: 2026-11-26
- Supersedes: none
- Superseded by: none

## Context

Today the dot acts under the owner's GitHub login ([0016](0016-awf-gh-runs-natively.md)).
That is fine alone and wrong for a team: teammates cannot tell whether the owner or the agent
wrote a comment, the owner cannot approve their own pull request through the dot, and
`reply.only_authors` (`internal/repos/features_builtin.go`) filters on a login the dot shares
with its owner.

What is built, and what is not:

- Nothing signs. No workflow adds a footer or a marker.
- There is no single emission point. Seven `gh pr comment`, `gh issue comment` and `gh pr review`
  calls sit in `workflows/review-pr.yaml`, `reply-pr.yaml`, `fix-issue.yaml`, `ask-pr.yaml` and
  `publish-held.yaml`. Each is a shell step, as ADR 0002 requires.
- `bin/publish-gate` is the one place that decides whether to publish, but it does not post, and
  it is called only when an `autonomy` ceiling is set (`docs/autonomy.md`). It is the wrong place
  for the signature.
- The dot's login is already a concept: `DOT_LOGIN` (default: the login `gh` is signed in as)
  scopes merges in `bin/publish-gate` and the breaker (`docs/breaker.md`).
- The store separates humans from bots (`actor.kind` in `internal/store/store.go`), so claims
  already test the actor, not the login.

Four ways to appear exist (owner's account with a signature, without, a GitHub App, a machine
account). They differ in setup cost (an App needs an organisation admin), in what the dot may do
(only a separate identity can approve the owner's pull request) and in honesty. See use cases A6,
B1, B2, D1.

## Decision

We add an `identity` key per repository, with a default under `defaults`:

- `signed` (default): the owner's account, plus a short footer and an invisible marker
  carrying the dot id, workflow and run.
- `silent`: the owner's account, no visible mark. The invisible marker stays, because it is
  what stops the dot from answering itself. Refused at trust `client` and `strict`
  ([0032](0032-trust-levels-per-repository.md)).
- `app`: a GitHub App installation token.
- `account`: a dedicated machine account.

Every write to GitHub already goes through a shell step ([0002](0002-shell-steps-own-side-effects.md)).
That step picks the token and adds signature and marker once, so no workflow knows the mode.
Commits may take their own mode: authored by the person who delegated, with the dot as
co-author, while comments stay marked as the dot's.

### Recommendation

Ship `signed` and `silent` only. Defer `app` and `account` until a team asks, since they need
token storage and an admin.

Smallest first step: one script, `bin/dot-sign`, that takes a comment body file and prints it with
the footer and the marker (`<!-- dots id=... workflow=... run=... -->`). The seven call sites pipe
their body through it. No key yet: every repository is `signed`. Then add `identity` to
`internal/repos` (one feature file, `features_identity.go`) with the trust check, and make
`only_authors` and the claim code ignore a comment that carries the marker.

### Options

| Option | What it costs | What it rules out |
| --- | --- | --- |
| A. `signed` only, one script (recommended) | one script, seven edits, no config | silent mode; approving the owner's own pull request |
| B. `signed` and `silent` with the `identity` key | A plus one feature file and a trust check | nothing new; `app` stays open |
| C. `app` first | App registration, token minting, rotation, an org admin | repositories where nobody can install an App |
| D. `account` first | a seat per organisation and an invitation | repositories where nobody can invite |

### Questions for the owner

1. Is `signed` the default everywhere, including your own repositories (pick: yes, or `silent` on
   your own and `signed` elsewhere)?
2. Is a "signed" footer on every comment acceptable on open source repositories that did not ask
   for it (yes or no)?
3. May `app` and `account` wait until a team member asks (yes or no)?

### Settled in discussion (2026-11-26)

- **No default mark.** `signed` is not the default everywhere. Marking the dot's actions is opt-in or
  opt-out, set per repository or by a profile ([0035](0035-a-dot-is-an-engine-instance-with-a-policy-profile.md)):
  a company profile may default to `signed` and a repository may opt out, a personal profile may leave it
  off. Without a setting, behaviour stays as it is today.
- Refusals stay: `silent` is refused at trust `client` and `strict`
  ([0032](0032-trust-levels-per-repository.md)).

## Consequences

Easier: the team cases (B1, B2), `/dots take` from the author of a pull request, telling dots
apart (the marker names the dot), and the dot never answering itself.

Harder: authorship filters must test the marker, not only the login; claims
([0026](0026-claims-and-presence.md)) already tell human from bot by actor, but a comment posted
under the owner's login still needs the marker to be told from the owner's own words; `app` and
`account` need token storage and rotation per identity.

This record is a direction. The marker script is small enough to ship before the key.

## Alternatives

Only a bot account: forces a seat and an invitation everywhere, including repositories where
nobody can invite. Only the signature: never lets the dot approve. A silent default: honest
users lose, and open source projects that require disclosure refuse it.
