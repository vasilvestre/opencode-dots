# 0048. Workers pull jobs from a queue, through one public entry

- Status: proposed
- Date: 2026-11-27
- Supersedes: 0033
- Superseded by: none

Relates to [0013](0013-dashboard-on-the-tailnet.md), which kept every port on
loopback, and to [0042](0042-workstations-are-workers-of-an-instance.md), whose two open questions this
answers. 0013 and 0042 are not rewritten.

## Context

The first design of an executor ([0031](0031-executor-per-repository.md)) was a push: the server's
watcher posts each event to the `awf serve` of a machine, which the server reaches through a reverse SSH
tunnel from that machine. It worked, but a developer had to run awf, a tunnel and an agent CLI, and a run
carried no claim: nothing said which machine took it, nothing stopped two from taking it, and a machine that
was off delayed an event with no record of it. The team has neither Tailscale nor a company VPN to join,
only SSH and NetBird, and a worker must work unattended, so it cannot rely on a person's sign-in session
(one hour, by [0046](0046-the-visitor-brings-their-own-token.md)).

0042 asked two questions: do workers only get listed, or pull tasks; and do they authenticate with an
instance token or with a tailnet identity. 0033 proposed a heartbeat table, an ordered list of executors
and a revision check to answer "who runs this".

## Decision

We make the queue the truth, and the workers pull from it.

**A queue instead of a push.** The watcher keeps posting a run the way it posts to awf, but to the
dashboard's `/queue` (`POST /queue/api/workflows/{scope}/{workflow}/run`, answered 202 with a job id, and
`DELETE /queue/api/executions/{id}` to cancel). The run is kept in `STATE_DIR/jobs.json` (mode 0600) until a
worker claims it. A worker claims a job; the first claim wins and the others are refused. The claim is a
lease of two minutes that the same worker renews, and the answer to a renewal tells it when the watcher
cancelled the run. A worker that vanishes gives the job back. An unclaimed job expires after 24 hours, so
a worker that comes back after days runs no stale work. A worker only sees the jobs of the repositories
its token names. The identity of a holder is the token's label plus an instance id, so two machines that
share one token never renew each other's claim.

**The hub wakes a worker, and the queue decides.** The dashboard publishes `job`, `taken`, `finished` and
`cancelled` events, as private updates, on the topic `dots/repo/<owner>/<repo>`. A worker follows its topics
and reads the queue on every message and every minute, so a missed message delays a job and never loses it.

**A token for a worker nobody watches.** The owner issues a worker token (`bin/worker-token`): a secret
shown once, kept only as a SHA-256 hash, bound to a label, a login and a list of repositories, valid 30 days
by default and 90 at most, revocable in one call. The worker trades it for hub tokens of 15 minutes (the hub
cuts a stream at the token's expiry, and the doc of Mercure puts the refresh in the client). A person who
signed in with GitHub gets the same hub token, limited to the repositories they may reach
([0046](0046-the-visitor-brings-their-own-token.md)), from `/api/hub-token`.

**One public entry.** The hub's own Caddy answers three paths and nothing else (the rest is 404): the hub
at `/.well-known/mercure`, which refuses an anonymous subscriber, and `/worker/*`, relayed to a dashboard
listener that serves only the worker routes, and its older alias `/worker-token`. Everything else of the
dashboard and the queue's enqueue route stay on loopback. A small SSH forward from the owner's reverse
proxy (Traefik, in this case) carries it to the hub, with a key that can open that one forward and run
nothing. This narrows 0013: one path with its own authentication is reachable from the Internet, and no
other port is.

**Answers to 0042.** Workers pull tasks, and they authenticate with a worker token. The tailnet identity the
record preferred does not exist for a team that has no tailnet.

**Cost and billing follow the run.** A worker reads what a run cost from its local dashboard and reports it
with the end of the job, so the server's Cost tab, Home page and history count it. A run with no reported
price counts as unpriced, never as free. Replaying a run a worker did enqueues it again.

## Consequences

- A developer needs one outbound HTTPS connection and a local awf; no tunnel, no open port, no tailnet.
- A job is run once, and a machine that is off delays a job and does not lose it. The dashboard of the
  server shows the runs of workers (the queue answers `/api/executions` and `/api/history` like an awf).
- The logs and steps of a run stay on the worker's machine; the server shows status, target, duration and
  cost, not the log.
- One more public path on the company's reverse proxy, protected by a 256-bit token and a half-second
  delay on a wrong one. The worker listener, the queue and the tokens are new code that guards the
  repositories of a client.
- A worker acts with the GitHub account of the machine it runs on, so what it posts carries that
  account (and the dot's marker, if the repository asks for one).
- 0033's revision check (a stale executor refuses a run) is not built; it stays a good idea for a later
  record. Its ordered list of executors and its heartbeat table are replaced by the queue.

## Alternatives

- **Keep the push and the reverse tunnel.** Works, but heavy to join and carries no claim.
- **Heartbeats and an ordered list (0033).** More moving parts for the same question ("who runs this"),
  and a lost heartbeat can still send a task to two machines.
- **Expose the dashboard, not a hub and a worker listener.** A far larger surface for a need that is one
  exchange.
- **A worker keeps a person's session.** It dies after an hour and ties the worker to a person at a screen.
