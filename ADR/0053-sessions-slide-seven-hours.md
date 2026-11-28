# 0053. A sign-in session slides for seven hours, up to a day

- Status: accepted
- Date: 2026-11-28
- Supersedes: none
- Superseded by: none

## Context

[0046](0046-the-visitor-brings-their-own-token.md) ends a session one hour after sign-in, so that a right removed
on GitHub disappears quickly. The rest of 0046 stands. In practice the device flow every hour interrupts a working
day.

## Decision

We change only the session length of 0046. A session ends after seven idle hours; each use renews it, and it never
outlives 24 hours from sign-in, when the rights are read again with a new token. The cookie lasts the 24 hours, the
server decides.

## Consequences

A day of work needs one sign-in. A right removed on GitHub can stay visible for up to 24 hours, not one. Renewals
are written to `sessions.json` at most once a minute of slide.

## Alternatives

A fixed longer session: simpler, but it logs out in the middle of work. Seven days: rights too stale.
