# 0003. Private names never enter git

- Status: accepted
- Date: 2026-11-23
- Supersedes: none
- Superseded by: none

## Context

This repository is public. It reads three files that name private repositories,
schedules and watched accounts: `awf-gh.yaml`, `repos.map` and `tend.list`. A
leak is permanent once pushed.

## Decision

We track only the `.example` twin of each private file. The real file is
gitignored, and the code that reads it accepts the path from an environment
variable so a missing file fails with a message rather than a silent default.

## Consequences

A fresh clone runs `make configure` and copies the examples, which doubles as
documentation. A new private input needs two files, the real one and its
example; forgetting the ignore rule is the only way to leak.

## Alternatives

Keep one file with placeholders and edit it in place. Rejected: one careless
`git add -A` publishes the real values. Encrypt the values in git. Rejected:
a key to hold, for three short files.
