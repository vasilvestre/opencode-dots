# 0042. A workstation is a worker that joins an instance

- Status: proposed
- Date: 2026-11-26
- Supersedes: none
- Superseded by: none

## Context

A developer who wants to work locally runs an `awf` and an agent on their own machine. For a
personal dot that is already the executor of [0031](0031-executor-per-repository.md): the server
watches and posts to the workstation through a tunnel. For a company's dot the direction is
wrong. A company does not want inbound connections to developers' laptops, and a laptop is on and
off, which [0033](0033-hybrid-executors.md) handles with a heartbeat.

A worker also brings its own models and its own account, so who pays
([0036](0036-model-budgets-and-caps.md)) and which repositories it may receive
([0032](0032-trust-levels-per-repository.md)) matter. See use cases A3, B3, D3.

## Decision

Open. Proposed shape: a worker **connects out** to its instance, registers a name and what it can
run (provider, models, repositories it holds), sends a heartbeat, and pulls the tasks it is
allowed to take. The instance never connects in. The executor allow-list of 0032 decides which
repositories a worker may ever receive: a `strict` repository is never sent to a workstation.

A task goes to one worker, chosen by the instance, as 0033 describes. When no worker is live, the
instance runs it if the profile allows, or the task waits and says so in the dashboard.

## Consequences

Easier: no tunnel to maintain, no open port on a laptop, and a laptop that sleeps simply stops
pulling.

Harder: the pull protocol is new, a worker's identity and revocation need a design, and the cost
a worker incurs on its owner's account must be visible to them.

## Alternatives

Keep pushing through a tunnel: fine for one person, unacceptable for a company. Run everything on
the server: loses local models, local clones and the developer's own keys.
