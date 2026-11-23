# 0001. Split agent memory into three stores

- Status: accepted
- Date: 2026-11-23
- Supersedes: none
- Superseded by: none

## Context

Every agent pass pays for every token it reads, and the repository checkout is
already in its context. One shared file for everything would grow without
bound, and a wrong line in it would damage every repository at once. The
measurements behind this choice are in `docs/memory.md`.

## Decision

We keep three stores. Each repository gets `state/tend/<repo>/lessons.md` for
its rules and `memory.md` for its facts. A pass that learns something true in
any project writes a candidate to `~/dreams/dreaming/inbox/`, and the nightly
pass folds it into `~/dreams/MEMORY.md`. Injected files are capped at 15 and 25
lines.

## Consequences

A rule stays close to the repository it came from. Sharing costs a review step,
so a global fact is never immediate. A bad line in the global file reaches every
repository; that is why the dot never writes it directly.

## Alternatives

One large per-repository file. Rejected: it grows and the pass reads all of it.
A single global file. Rejected: concrete, repository-specific rules hurt when
moved, as the measurements show.
