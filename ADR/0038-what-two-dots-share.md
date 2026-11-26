# 0038. Two dots on one repository share metadata and reviewed lessons, never code or raw memory

- Status: proposed
- Date: 2026-11-26
- Supersedes: none
- Superseded by: none

## Context

[0029](0029-independent-servers-and-contexts.md) keeps servers independent, which is right for
confidentiality. People still run two dots: a personal one and a team's, or one per teammate
([0035](0035-a-dot-is-an-engine-instance-with-a-policy-profile.md)). Without a rule they meet on the
same repository: a double review, contradictory comments, two pushes. Memory per workspace has one
writer ([0025](0025-workspace-memory-single-writer.md)). See use cases C1 to C5.

What is built, and the gap in it:

- Claims exist (`docs/claims.md`, `internal/store/claims.go`) with heartbeats, a time to live and
  an `actor_id` whose kind is `human` or `bot`.
- The rule there is: "A bot's own claim never vetoes the bot." A second dot is a bot, so under
  today's rule its claim would not veto the first dot. That is the collision this record fears, and
  the code already settles part of it: claims cannot be the answer between two dots as they are.
- `internal/events/mercure.go` publishes `created`, `renewed` and `released` to a hub, tested
  against the real hub, but the hub is local to one server.
- Recognising another dot needs the marker of [0034](0034-dot-identity-in-github.md), which is not
  built.
- `repos.yaml` has no key naming "the repository's dot".

## Decision

Open. Proposed shape: dots exchange only what is needed to avoid collisions and to learn:

- a claim by a dot on a repository or pull request, with the dot id, treated like a human claim by
  every other dot (the owner of the repository's dot comments, the other stays quiet);
- state metadata ("review pending"), never content;
- lessons, only after a human reads and accepts them.

Code, diffs, raw memory and worklogs never cross a trust boundary. A dot recognises another by the
marker of 0034 and defers to the one named in `repos.yaml` as the repository's dot.

### Recommendation

Do the one-key version and skip the exchange channel: a `dot` key per repository in `repos.yaml`
naming the dot that acts there, and every other dot treats that repository as observe-only
([0040](0040-classification-and-autonomy-ceiling.md)). It needs no network between servers, and
solves C1 and C2 by configuration.

Smallest first step: add `dot` to `internal/repos` (one feature file) and have the generated
`awf-gh.yaml` drop every rule for a repository whose `dot` is not this server's id. Leave the claim
channel and shared lessons for a later record.

First step built: `settings.dot_id` and the per-repository `dot` key (`internal/repos/features_dot.go`,
`docs/dots.md`). A repository whose `dot` differs from this server's id is left out of `awf-gh.yaml`,
`tend.list`, `repos.map`, `sync.map`, `breaker.list` and any executor watcher file, and each generated
file says how many were skipped, never which. `dot` without `dot_id` is refused. A configuration
without the key generates byte-identical files. There is still no exchange between servers; the claim
channel, shared lessons and the `bin/health` warning remain open, so this record stays proposed.

### Options

| Option | What it costs | What it rules out |
| --- | --- | --- |
| A. A `dot` key; the others stay out (recommended) | one feature, one server id | two dots reviewing the same repository on purpose |
| B. Let another dot's claim veto the bot | change the claim rule, a bus between servers | independent servers (0029) |
| C. Exchange state metadata over a bus | a channel, auth between servers | simple deployments |
| D. Shared reviewed lessons | a review step and storage | nothing, but needs A first |

### Questions for the owner

1. Is "one acting dot per repository, named in `repos.yaml`" enough for C1 and C2 (yes or no)?
2. Does the server need an id of its own in `repos.yaml` to compare against (pick: a new id, or the
   hostname)?
3. May shared lessons wait until after the first two-dot setup exists (yes or no)?

## Consequences

Makes C1 and C2 safe and C5 (moving a repository) a defined operation: change one key on two
servers. Rules out a global memory. A forgotten key leaves two dots acting, so `bin/health` should
warn about it once a second server exists.

## Alternatives

A single shared memory: leaks across clients. No exchange at all: collisions stay possible.
