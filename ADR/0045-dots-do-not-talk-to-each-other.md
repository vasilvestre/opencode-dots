# 0045. Dots do not talk to each other; the owner keeps a repository on one dot

- Status: accepted
- Date: 2026-11-27
- Supersedes: 0038
- Superseded by: none

## Context

[0038](0038-what-two-dots-share.md) asked what two dots on one repository may share, and its first
proposal had them exchange claims, state metadata and reviewed lessons. Its Recommendation was already the
smaller version: no channel between servers, and a `dot` key that names the one dot acting on a repository. That
key is built (`settings.dot_id` and the per-repository `dot`, `docs/dots.md`).

The owner's decision (2026-11-27): dots are not able to talk to each other, and it is up to the person to take
care not to put the same repository on two dot instances. Servers stay independent, as
[0029](0029-independent-servers-and-contexts.md) already says; the owner does not want a protocol between them to
build, secure and keep in step.

## Decision

We build no exchange between dots: no claims, no state and no lessons travel from one dot to another, and no dot
reads another's memory or database. Two dots never coordinate.

The owner is responsible for a repository belonging to exactly one dot. Putting the same repository on two
instances is a mistake that nothing here detects or prevents; the cost of it (two reviews, two pushes) falls on
the owner who made it.

The `dot` key and `settings.dot_id` stay as they are: an optional note the owner writes in `repos.yaml` so that a
server skips the repositories declared to belong to another dot. It exchanges nothing, checks nothing against the
other server, and a configuration without it behaves as before.

## Consequences

Easier: no protocol, no shared secret, no shared memory between servers, and the confidentiality boundary of 0029
needs no exception. Moving a repository from one dot to another stays an owner act: change the key on both sides.

Harder: nothing warns an owner who lists a repository on two dots. A dot also learns nothing from another dot's
lessons, so the lessons of a repository stay on the dot that holds it.

Rules out: the claim channel between servers, shared lessons and any dot-to-dot call. A later record may reopen
it if the owner changes their mind.

## Alternatives

A channel for claims and shared lessons, as 0038 first proposed: it solves duplicated repositories by software
instead of by care, at the price of a protocol between independent servers. A check at generation time that two
`repos.yaml` files do not overlap: it needs both files in one place, which the independence of the servers rules
out.
