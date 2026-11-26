# 0042. A workstation is a worker that joins an instance

- Status: proposed
- Date: 2026-11-26
- Supersedes: none
- Superseded by: none

## Context

A developer who wants to work locally runs an `awf` and an agent on their own machine. For a
personal dot that is already the executor of [0031](0031-executor-per-repository.md): the server
watches and posts to the workstation through a tunnel (`docs/executors.md`, `docs/remote.md`). For a
company's dot the direction is wrong. A company does not want inbound connections to developers'
laptops, and a laptop is on and off.

What is built that a worker can reuse:

- Presence with a heartbeat: a claim carries `machine`, `renewed_at` and `expires_at`, defaults to 90
  seconds and expires by itself when heartbeats stop (`docs/claims.md`). That is the on and off
  signal of [0033](0033-hybrid-executors.md), already in the store.
- A hub that tells listeners about changes (`internal/events/mercure.go`).
- Health of executors: `bin/health` checks that each executor in `repos.yaml` answers
  (`docs/health.md`), and the dashboard polls remote runs (`/api/remote-runs`). Both are the server
  calling the executor, the wrong direction for a company.
- A sandbox for untrusted steps (`docs/runner.md`) that a worker can use for code from others.
- Not built: a worker registry, a pull endpoint, a worker identity, any revocation.

A worker also brings its own models and its own account, so who pays
([0036](0036-model-budgets-and-caps.md)) and which repositories it may receive
([0032](0032-trust-levels-per-repository.md)) matter. See use cases A3, B3, D3.

## Decision

Open. Proposed shape: a worker **connects out** to its instance, registers a name and what it can
run (provider, models, repositories it holds), sends a heartbeat, and pulls the tasks it is allowed
to take. The instance never connects in. The executor allow-list of 0032 decides which repositories a
worker may ever receive: a `strict` repository is never sent to a workstation.

A task goes to one worker, chosen by the instance, as 0033 describes. When no worker is live, the
instance runs it if the profile allows, or the task waits and says so in the dashboard.

### Recommendation

Do not build the pull protocol first. Prove the model with what exists: a worker registers as a `bot`
actor with a machine name, holds a heartbeat the same way a claim does, and the instance only
lists live workers in the dashboard. No task is sent yet.

Smallest first step: `bin/repos db worker register|heartbeat` over the existing store (a `worker`
table with name, machine, capabilities as JSON, expires_at) and a read-only Workers card on the
dashboard. Pulling tasks is the next step, once we know how a laptop really behaves.

### Options

| Option | What it costs | What it rules out |
| --- | --- | --- |
| A. Register and heartbeat first, no tasks (recommended) | one table, one command, one card | running company tasks on laptops, for now |
| B. Full pull protocol now | an endpoint, auth, a queue, revocation | a small first step |
| C. Keep the tunnel | nothing | any company dot |
| D. Run everything on the server | nothing new | local models, local clones, the developer's own keys |

### Questions for the owner

1. Is a company laptop allowed to pull tasks at all in the first version, or only be listed (pick:
   listed only, or pull)?
2. Does a worker authenticate with a token the instance issues, or by its tailnet identity (pick
   one)?
3. May the cost a worker incurs on its owner's account stay invisible until 0036 is decided (yes or
   no)?

### First step built

Presence only: a `worker` table (name, machine, capabilities as JSON, registered, renewed and
expiry times, a `bot` actor), `bin/repos db worker register|heartbeat|list|expire` with the
claim time to live (90 s, at most one hour, server clock), a read-only `GET /api/workers` and a
Workers card on the Repos view. No task is pulled and no protocol or authentication exists yet,
so the card lists machines that cannot do anything. This ADR stays proposed. See `docs/workers.md`.

## Consequences

Easier: no tunnel to maintain, no open port on a laptop, and a laptop that sleeps simply stops
pulling.

Harder: the pull protocol is new, a worker's identity and revocation need a design, and the cost a
worker incurs on its owner's account must be visible to them. Starting with presence only means the
dashboard shows workers that cannot yet do anything, which can mislead.

## Alternatives

Keep pushing through a tunnel: fine for one person, unacceptable for a company. Run everything on the
server: loses local models, local clones and the developer's own keys.
