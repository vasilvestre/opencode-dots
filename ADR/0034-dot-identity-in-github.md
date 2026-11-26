# 0034. The dot says who it is in GitHub, and the identity is a per-repository choice

- Status: proposed
- Date: 2026-11-26
- Supersedes: none
- Superseded by: none

## Context

Today the dot acts under the owner's GitHub login ([0016](0016-awf-gh-runs-natively.md)).
That is fine alone and wrong for a team: teammates read a comment and cannot tell whether
the owner or the agent wrote it, the owner cannot approve their own pull request through
the dot, and `reply.only_authors` filters on a login the dot shares with its owner, so the
dot can mistake its own message for the owner's.

Four ways to appear exist, and none is right everywhere: the owner's account with a visible
signature, the owner's account with no mark, a GitHub App, a dedicated machine account. They
differ in setup cost (an App needs an organisation admin), in what the dot may do (only a
separate identity can approve or request changes on the owner's pull request) and in honesty
(an unmarked comment passes a machine for a person). See use cases A6, B1, B2, D1.

## Decision

We add an `identity` key per repository, with a default under `defaults`:

- `signed` (default): the owner's account, plus a short footer and an invisible marker
  carrying the dot id, workflow and run.
- `silent`: the owner's account, no visible mark. The invisible marker stays, because it is
  what stops the dot from answering itself. Refused at trust `client` and `strict`
  ([0032](0032-trust-levels-per-repository.md)).
- `app`: a GitHub App installation token.
- `account`: a dedicated machine account.

The default is `signed`. A repository that names no `identity` and inherits none under
`defaults` gets the footer and the marker. `silent` is a real mode, for a repository whose
owner decided that a visible footer is noise, and it stays refused where a client or a
strict level is set: an unmarked comment there would pass a machine for a person.

Every write to GitHub already goes through a shell step ([0002](0002-shell-steps-own-side-effects.md)).
That step picks the token and adds signature and marker once, so no workflow knows the mode.
Commits take their own mode at the same level, `commits` under a repository: `delegate` (the
default) authored by the person who delegated the work, with the dot as co-author, and
`signed` keeps the dot as author with the footer and the marker. A comment is always marked
as the dot's, whatever the commit mode is.

Authorship filters read the marker. An event whose author login is the dot's and whose body
carries the dot's marker is the dot's, and `only_authors` and `ignore_authors` treat it as
the dot, not as the login. A `/dots take` in a comment counts only when the command is the
whole body after the marker is removed, so the dot never delegates to itself.

## Consequences

Easier: the team cases (B1, B2), `/dots take` from the author of a pull request, approving a
person's pull request, telling dots apart (the marker names the dot).

Harder: authorship filters must test the marker, not only the login; claims
([0026](0026-claims-and-presence.md)) must tell the human from the bot by actor, not login;
`app` and `account` need token storage and rotation per identity.

The marker is what a filter tests. A login alone cannot tell the owner from the dot when the
dot runs under the owner's account, so every step that reads an author tests the marker, and a
comment without it written by the owner's login is the owner's.

This record is a direction. It is not scheduled.

## Alternatives

Only a bot account: forces a seat and an invitation everywhere, including repositories where
nobody can invite. Only the signature: never lets the dot approve. A silent default: honest
users lose, and open source projects that require disclosure refuse it.
