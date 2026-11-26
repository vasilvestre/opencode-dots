# 0033. An executor can be a list, and what every executor runs is checked

- Status: proposed
- Date: 2026-11-26
- Supersedes: none
- Superseded by: none

## Context

[0031](0031-executor-per-repository.md) lets a repository name one executor: the `awf serve`
that runs its tasks while the server only watches. It left three questions open, and the first
use of it showed that they are real.

- **Which machine runs a task.** One name is one machine. When the workstation is off, `awf-gh`
  retries the event every pass and the task waits. A second machine that could run it, such as the
  server's own worker when there is one, has no way to be tried.
- **Which workflows run.** An executor runs the workflow files of its own clone. The watcher expects
  a revision. Nothing says when they differ, and `awf` lists a `version` per workflow that is `1.0.0`
  everywhere and never changes.
- **Which models run.** The model of each role is written into the workflow files
  ([0018](0018-dashboard-managed-model-presets.md), [0019](0019-claude-model-presets.md)), and the
  dashboard edits those files. Two machines drift as soon as each edits its own. This was not
  hypothetical: the same pull request was dispatched twice, once to each machine's watcher, and the
  two runs used different providers because the two `repos.yaml` files disagreed.

One fact makes the last question cheaper. `awf-gh` merges the `inputs` of a rule into every dispatch,
and the workflow ignores an input it does not declare (read in its source, `config.go`). A rule can
therefore carry the model of a role for one executor without touching a workflow file.

## Decision

We extend the executor in three steps that do not depend on each other.

**A list, tried in order.** `executor` may name several executors, such as `[workstation, server]`.
The generator writes the rules of a repository once per executor, and the watcher that owns an executor
only dispatches when that executor answers. Which one wins is the answer to a question that needs a
signal better than "the dispatch failed": an executor publishes a heartbeat, and the claim machinery of
[0026](0026-claims-and-presence.md) is the model, with the server's own clock and a short time to live.
A task goes to the first executor of the list whose heartbeat is live, and to the next after a delay the
owner sets. The decision is kept per event, so a task is never dispatched to two executors.

**An executor announces itself.** It publishes its name, its address, the revision of its clone and its
`awf` version to the control plane ([0024](0024-control-plane-database.md)) with each heartbeat, instead
of being listed by hand in `repos.yaml`. `settings.executors` stays as the way to name one before it has
announced itself.

**The revision is checked, the models are passed.** The watcher dispatches with the revision it expects,
as an input every workflow declares. A workflow compares it with its own clone's revision in its first
step and stops with a clear message on a difference, so a stale executor fails early and visibly. The
models are no longer read from the executor's files: the generated `awf-gh.<executor>.yaml` carries them
as `inputs` of each rule, from the presets in `repos.yaml`. The files then hold no machine-specific value,
and the dashboard edits the presets, not the workflows.

None of this changes a repository that names one executor, or none.

## Consequences

- A task can run while the first-choice machine is off, if a second one is live, and never runs twice.
- A machine with an old clone refuses early instead of running an older workflow.
- Models stop drifting between machines, and the workflow files lose the one value that made them differ.
- More moving parts: a heartbeat to send, a table to read, a delay to choose. A heartbeat lost for a short
  time can send a task to the second machine while the first is still working. The delay and a live check
  at dispatch narrow that and do not close it.
- Passing the models as inputs replaces the eager projection of 0018 and 0019, and so supersedes part of
  them. That needs the dashboard to write presets, not workflow defaults, which is a change to it.
- The revision check needs every workflow to declare an input and to do the comparison, in the same
  shape, or the check is incomplete.

## Alternatives

- **One executor per repository, always.** Simple, and a task waits for as long as the machine is off.
- **A queue that executors pull from.** No heartbeat and no routing, and a different system: a queue, an
  API and a local agent. It is the next step if routing grows (0031 names it), and the executor name in
  `repos.yaml` hides the mechanism, so it can replace this one.
- **Pin the revision with `awf`'s workflow `version`.** It is already there and no one bumps it. A manual
  version drifts from the code it names, and a revision cannot.
- **Keep the models in the workflow files and sync the files.** It keeps the dashboard as it is and makes
  two machines edit one file, which is the drift again.

## Open questions

- Where the revision is read from on each side, and what a workflow without git (an image) reports.
- The delay before the second executor is tried, and whether it is a setting of the repository or of the
  executor.
- Whether the heartbeat travels through the tunnel of 0031 or its own channel, and what it costs on a
  laptop that sleeps.
- How the dashboard shows an executor that is behind, away, or in disagreement with the watcher.
- Whether to send the revision to workflows that do not read it yet, or to wait until all of them do.
