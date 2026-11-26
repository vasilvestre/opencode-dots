# 0023. A work item is independent of the tracker

- Status: proposed
- Date: 2026-11-26
- Supersedes: none
- Superseded by: none

## Context

Every part of the dot identifies its subject by repository and number. State
lives under `replies/pr<N>`, memory of a pull request is keyed by repository and
number ([0006](0006-per-pull-request-memory.md)), `awf-gh` deduplicates on a
repository, an event and a number, and an issue carrying the `dot` label is the
trigger of `fix-issue`. A GitHub issue or pull request is therefore the unit of
work, by habit rather than by decision.

That habit breaks on three things we want next:

- A branch or a pull request can exist with no issue. Nothing names that work
  until a pull request exists, and one issue can lead to several pull requests
  (a fork, a retry, a split).
- Jira will carry the issue while GitHub carries the branches and pull
  requests. The key `ABC-12` and the pull request that implements it describe
  the same work and have no common name.
- Checking out a subject locally, claiming it, and pausing the bot on it all
  need one identifier for "this piece of work", and each must work whichever
  tracker the work came from.

Without a shared identity, each of these features would re-derive its own link
between an issue, a branch and a pull request, and the links would disagree.

## Decision

We introduce the **work item** as the unit of work, independent of any tracker.
A work item has an opaque stable id, a workspace, a project (the repository),
a title, a state, the actor that created it, timestamps, and a list of
references. A reference is `tracker:key`: `github:owner/repo#12` for an issue or
a pull request, `branch:owner/repo:name` for a branch, `jira:ABC-12` later.

Rules:

- A work item may have no tracker issue. It is created by the first event that
  names a reference not yet attached to any work item, a branch or a pull
  request included.
- A reference belongs to one work item at a time. Merging two work items or
  splitting one is an explicit action, never an inference.
- References are attached from structured fields only: the head branch of a pull
  request, the tracker's own link between an issue and a pull request. We never
  parse free text for them, because text in an issue or a pull request is data,
  not instruction.
- Each tracker is an adapter with two jobs. Inbound, it turns a tracker event
  into a common envelope: reference, kind, actor, time, payload. Outbound, it
  posts a comment or a transition, from a shell step, as in
  [0002](0002-shell-steps-own-side-effects.md). The agent never calls a tracker
  write.
- Every record that names a person carries an actor identifier, from the first
  version. One user today does not remove the field.

The first version has a GitHub adapter only. Existing workflows keep their
repository and number keys: the work item is added as a mapping from those keys,
and `state/` is not migrated. Jira is a second adapter, later.

This record does not decide where work items are stored (the database that
replaces `repos.yaml`, a later record), how memory follows a subject, or how a
claim on a work item works. Each of those depends on this one.

## Consequences

- One name for a piece of work, whatever its tracker. Claims, checkouts and
  pauses attach to it instead of to an issue.
- Work with no issue is first class, and one issue with several pull requests
  stays one work item.
- A second identity to keep consistent with the tracker's. A wrong link, such as
  a branch attached to the wrong item, now produces wrong behaviour on both,
  and only an explicit merge or split corrects it.
- `awf-gh` is a separate project and keeps its repository, event and number
  identity. The mapping to work items sits on our side, so a tracker with a
  many-to-many link, as Jira can have, may force changes upstream.
- A mapping layer lets existing runs and history continue unchanged, and costs a
  lookup on each event until workflows use the id directly.

## Alternatives

- **Keep the GitHub issue as the unit.** Nothing to build, but it cannot name
  work without an issue and cannot join a Jira key to its pull requests.
- **Use the pull request as the unit.** It exists late, and one issue can have
  several, so it cannot name the work before code exists.
- **Use the branch name as the key.** Branches are renamed, and
  [0020](0020-fix-issue-works-from-a-fork.md) already works from forks, where
  the same branch name lives in two repositories.
- **Use the tracker key as the id, with a prefix.** Simple, and it still cannot
  name a branch with no issue, or two trackers describing one piece of work.
