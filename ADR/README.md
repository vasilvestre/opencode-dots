# Architecture decision records

One file per decision that shaped this project. A record states what we chose,
why, and what it costs. It is history: never rewrite an accepted record. Change
your mind by writing a new one that supersedes it.

## Index

| Number | Title | Status | Date |
| --- | --- | --- | --- |
| [0001](0001-memory-split-in-three.md) | Split agent memory into three stores | accepted | 2026-11-23 |
| [0002](0002-shell-steps-own-side-effects.md) | The agent never writes to a remote; shell steps do | accepted | 2026-11-23 |
| [0003](0003-private-config-not-in-git.md) | Private names never enter git | accepted | 2026-11-23 |

## When to write one

Write a record before the code when any of these is true:

- the change needs a choice between two reasonable designs;
- an issue needs more than about forty lines, a new dependency, or a new
  interface;
- the change touches memory, state layout, security, or what the agent may do
  on its own;
- you expect someone to ask "why is it like this?" in six months.

A bug fix that only restores intended behaviour needs no record.

## Lifecycle

`proposed` → `accepted` → `superseded by NNNN`.

Keep `proposed` only while the discussion is open. A record that nobody acts on
becomes `rejected` with the reason in one line.

To supersede, write the new record and set both fields:

- the new file gets `Supersedes: NNNN`;
- the old file gets `Superseded by: NNNN` in its status and in its header. Do
  not edit anything else in the old file.

## Commands

```bash
bin/adr new drop-docker-mode   # create ADR/0004-drop-docker-mode.md
bin/adr check                  # index, numbering and supersede links agree
```

`bin/adr check` runs in `make adr` and in `make validate`.

## Issue too large

When an issue needs a design decision, the fix workflow does not guess. The
agent writes a draft record into its answer, and the draft is posted as a
comment on the issue. To turn it into a record:

```bash
bin/adr new <slug>
# paste the draft under Context and Decision, then set Status: proposed
```

The record is accepted when the maintainer says so in the issue.
