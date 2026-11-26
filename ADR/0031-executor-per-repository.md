# 0031. A repository names the executor that runs its tasks

- Status: proposed
- Date: 2026-11-26
- Supersedes: none
- Superseded by: none

## Context

The first always-on server ([0027](0027-eu-vps-native-local-mode.md)) runs the
daily `tend` pass. It has no worker for the other workflows (`review-pr`,
`reply-pr`, `fix-issue`, `fix-ci`): the MVP has no sandbox and no micro VM, so a
heavy run on a 1 GB machine is out of reach.

The owner already has a machine that does the work: the workstation, with its own
`opencode`, its own Claude and its own GitHub identity. We want the server to
notice the events and the workstation to run the tasks, so the repositories are
watched while the workstation sleeps and handled when it is on.

`awf-gh` fits this with no change. It polls GitHub and POSTs each event to an
`awf serve`, and its source shows what happens when that POST fails: the event is
not recorded as seen, so the next pass retries it. A workstation that is off
delays the event and does not lose it. Two limits apply. `awf_url` is global to
one `awf-gh` configuration, not per repository. The `awf` API has no
authentication.

Later, a worker could pull its tasks from a queue instead of being pushed to. That
is a larger piece of work and is not decided here.

## Decision

We let each repository name its **executor**: the `awf serve` that runs its tasks.
The server only watches.

In `repos.yaml`, `settings.executors` maps a name to an `awf` address, and a
repository takes a name with the key `executor`. A repository with no `executor`
runs its tasks on the `awf` of the machine that watches, as today. The address is a
machine setting, like `awf_url`, and stays out of tracked files
([0003](0003-private-config-not-in-git.md)).

`awf-gh` takes one `awf_url` per configuration, so the generator writes one
configuration per executor: `awf-gh.yaml` for the default, and
`awf-gh.<executor>.yaml` for each named one, each with its own `state_path` so the
seen markers do not mix. One `awf-gh` pass runs per generated file. The files exist
only when a watch rule needs them ([0030](0030-watcher-is-opt-in.md)).

The executor does the writing. Its steps post comments, push and open pull
requests with the identity that is logged in there. The token on the watching
server only has to read pull requests and issues, so it can be read-only.

The executor reaches the watcher, not the other way round. The `awf` API has no
authentication, so it is never exposed on a network. The executor opens an SSH
tunnel to the watching server (`systemd/dots-executor-tunnel.service`) that
publishes its `awf serve` on a port of the server's own loopback, and the
generated `awf-gh.<executor>.yaml` points at that port. The executor needs no
inbound port, the tunnel is authenticated by its SSH key, and only a process on
the server can reach the API. When the executor is off or asleep, the tunnel is
down and the dispatch fails, which `awf-gh` retries.

## Consequences

- Repositories are watched around the clock, and their tasks run on the owner's
  machine with the owner's tools. No worker or micro VM is needed for the MVP.
- An event whose executor is off waits and is retried every pass. When the
  executor comes back, the backlog is dispatched, bounded by `max_per_run` for
  each pass.
- The watching server holds a read-only token, so its compromise cannot write to a
  repository through the watcher.
- One `awf-gh` configuration and timer per executor. Two executors double the
  passes, and each pass is a cheap GitHub poll.
- The executor's `awf` API becomes reachable from every process of the watching
  server's user, through the tunnel. Whoever controls that server can run tasks on
  the executor, so the two machines share one trust boundary.
- A run still needs the executor's agent credentials. When the workstation's
  Claude window is spent, the run fails there, which is
  [P3](https://github.com/vasilvestre/opencode-dots/issues/29)'s concern.
- Cancelling a superseded run goes through the same address, so it works as long as
  the executor is reachable.
- Several people, each with their own machine, mean several executors. That fits
  the name-to-address map, and is not worked out here.

## Alternatives

- **A queue that workers pull from.** No inbound port on the workstation, works
  while it sleeps, and one worker or a server worker take the same tasks. It needs
  a queue, an API and a local agent, so it is a later step. Naming the executor in
  `repos.yaml` hides the mechanism, so it can replace this one.
- **A per-repository `awf_url` in `awf-gh`.** One configuration for everything. It
  changes a separate project, and the generated files here already carry the
  difference.
- **Expose the executor's `awf` on its tailnet address.** No tunnel to keep alive,
  but the API is then open to every device the access rules let through, and it has
  no authentication of its own. The tunnel keeps it off the network.
- **Run everything on the server.** It needs the memory, and the workers this MVP
  leaves out.
- **Edit several `awf-gh` configurations by hand.** It works today and breaks the
  rule that generated files come from `repos.yaml`
  ([0022](0022-repos-yaml-is-the-source-of-truth.md)).

## Open questions

- The hybrid case. A repository may be served by the server's own worker or by
  the owner's machine, whichever is online. The name in `executor` would then
  resolve to an ordered list, with a fallback when the first one does not answer
  in time. Availability needs an answer better than "the dispatch failed": a
  heartbeat that an executor publishes, the claim of
  [0026](0026-claims-and-presence.md) being one candidate.
- How an executor publishes its `awf` as a service on the host that runs it
  (a user unit, as `dots-awf.service` already is, and an address on the tailnet),
  and whether it announces itself (name, address, version, load) to the control
  plane ([0024](0024-control-plane-database.md)) instead of being listed in
  `repos.yaml` by hand.
- How the workflows stay the same on every executor. The definitions come from
  this repository, so an executor must run the revision the watcher expects, and
  a mismatch must be visible. The model defaults are a second problem: they are
  written into the workflow files
  ([0018](0018-dashboard-managed-model-presets.md),
  [0019](0019-claude-model-presets.md)), so two machines drift apart as soon as
  each edits its own. The per-executor `inputs` of a generated `awf-gh`
  configuration may carry them instead.
- Whether a long-lived SSH tunnel is robust enough on a laptop that sleeps and
  changes network, or needs a supervisor such as `autossh`.
- Which steps need the server's `gh` and which the executor's, when `awf-gh` and
  the workflows run on different machines.
- How the generated units are named and installed, one timer per executor.
- What happens to a run in progress when the dispatch of a later event fails.
- How an executor tells the owner that it is behind, when its backlog is large.
