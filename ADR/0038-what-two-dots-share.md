# 0038. Two dots on one repository share metadata and reviewed lessons, never code or raw memory

- Status: proposed
- Date: 2026-11-26
- Supersedes: none
- Superseded by: none

## Context

[0029](0029-independent-servers-and-contexts.md) keeps servers independent, which is right for
confidentiality. People still run two dots: a personal one and a team's, or one per teammate
([0035](0035-team-shapes-one-dot-per-person-or-team.md)). Without a rule they meet on the same
repository: a double review, contradictory comments, two pushes. The claim machinery
([0026](0026-claims-and-presence.md)) stops a bot pushing over a human, not a bot over a bot.
Memory per workspace has one writer ([0025](0025-workspace-memory-single-writer.md)).
See use cases C1 to C5.

## Decision

Open. Proposed shape: dots exchange only what is needed to avoid collisions and to learn:

- a claim by a dot on a repository or pull request, with the dot id, treated like a human
  claim by every other dot (the owner of the repository's dot comments, the other stays quiet);
- state metadata ("review pending"), never content;
- lessons, only after a human reads and accepts them.

Code, diffs, raw memory and worklogs never cross a trust boundary. A dot recognises another by
the marker of [0034](0034-dot-identity-in-github.md) and defers to the one named in
`repos.yaml` as the repository's dot.

## Consequences

Makes C1 and C2 safe and C5 (moving a repository) a defined operation. Needs a channel for
claims between servers (the Mercure bus of ADR 0026 is a candidate) and a review step for
shared lessons. Rules out a global memory.

## Alternatives

A single shared memory: leaks across clients. No exchange at all: collisions stay possible.
