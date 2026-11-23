# 0002. The agent never writes to a remote; shell steps do

- Status: accepted
- Date: 2026-11-23
- Supersedes: none
- Superseded by: none

## Context

The workflows read untrusted text: pull request bodies, review comments, diffs,
issue threads. A model that can push, comment or merge acts on that text
directly, and the only guard left is the prompt.

## Decision

We split writing from deciding. The agent step edits files inside one worktree
and nothing else. A later shell step commits, pushes, comments or merges, and
only after its own guard passes: no push to `main`, no push outside the owned
branch, no merge without a `merge` verdict.

## Consequences

A prompt injection cannot reach the network. The cost is that every new side
effect lives in YAML shell, not in the prompt, and a declined action must exit 0
so a guard never looks like a failure.

## Alternatives

Give the model `gh` and a token, with the prompt as the rule. Rejected: the
prompt is data, not a boundary. Teach the model to write a script and run it.
Rejected: the same reach with more moving parts.
