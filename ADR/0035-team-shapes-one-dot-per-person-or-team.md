# 0035. A dot serves a person or a team, and we name the shapes we support

- Status: proposed
- Date: 2026-11-26
- Supersedes: none
- Superseded by: none

## Context

The dot was built for one person ([0027](0027-eu-vps-native-local-mode.md)).
`docs/colleagues.md` is a draft of what a colleague gets and lists open questions. Two shapes
look similar and have opposite needs:

- **One dot per person.** Each developer runs their own, on their machine or server. Private
  by construction, nothing to administer, but lessons and configuration drift and two dots can
  meet on one repository.
- **One dot per team.** A shared server and a shared charter. Consistent, but it needs an
  identity ([0034](0034-dot-identity-in-github.md)), roles ([0037](0037-dashboard-users-and-roles.md))
  and a payer ([0036](0036-model-budgets-and-caps.md)).

See use cases B1 to B6 and C3.

## Decision

We have not decided. The question to answer: which of the two do we build for first, and is
the other explicitly out of scope for now. Whichever we pick, a repository belongs to exactly
one team dot at a time, and a person's own dot only adds private work on top of it
([0038](0038-what-two-dots-share.md)).

## Consequences

Choosing per person keeps the project small and defers 0036 and 0037. Choosing per team makes
0034, 0036 and 0037 prerequisites. Either way, `docs/colleagues.md` should say which one it
describes.

## Alternatives

Supporting both at once from the start: doubles the surface before either has a user.
