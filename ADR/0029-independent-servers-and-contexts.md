# 0029. Servers are independent and each device picks a context

- Status: proposed
- Date: 2026-11-26
- Supersedes: none
- Superseded by: none

## Context

[0027](0027-eu-vps-native-local-mode.md) puts the dot on one always-on server,
and [0028](0028-deployment-is-code-in-two-layers.md) makes a server something
we can rebuild. One server is not enough for long. The owner works in two
different trust settings:

- **Personal.** The owner's own repositories and tools, under the owner's
  identity and account, with a token that covers them. This is what runs today.
- **Enterprise.** The employer's repositories and the clients' repositories.
  Other people take part, the data belongs to someone else, and the clients may
  require their own way in: a company network, or a bastion with MFA, as in the
  existing deployment of the employer's own agent platform.

The two cannot share a machine. A token that is right for one is wrong for the
other: the owner's token does not work on a repository owned by a third party,
and a narrow token for the enterprise must not reach personal work. Data on a
personal server is under the owner's control, while data on an enterprise server
is under the company's and the client's.

The way in also differs. A device can hold several tailnet logins, but only one
is active at a time, and two tailnets can give two machines the same `100.x`
address. A device cannot rely on being on a personal tailnet and a company
tailnet together.

Repositories will also move. Work that sits on a server today will end up on
another one once both exist and are connected. Without a rule, a move means
copying `state/` by hand, as was done once already, and leaving files that
should not stay.

## Decision

We run one server per trust setting, and the servers are independent. Each has
its own database ([0024](0024-control-plane-database.md)), its own `state/`, its
own list of repositories, its own credentials and its own access rules. Nothing
is replicated between servers. A repository, its memory and its work items
belong to one server at a time.

Each device chooses a **context**: the address of one server, how to reach it
and which credentials to use. It is the same idea as the contexts of a
cluster client. A device holds several contexts and uses one at a time. The
dashboard and `opencode --server` follow the chosen one. A device is not asked
to join two networks to use two servers.

Access has two modes, and a server uses one of them:

- **Personal mode.** One tailnet, the owner's, with a tag per server and access
  rules written in code. The personal server and the owner's devices are in it.
- **Enterprise mode.** The server is reached through the means its owner
  requires: the company's own tailnet, or an SSH path through a bastion with MFA.
  An enterprise server does not join the personal tailnet, and the personal
  server does not join the company's. A colleague gets access to one server and
  does not see the owner's other machines.

What may cross between servers is decided by a person, not by a sync. The one
case that exists today is a durable rule that holds beyond its repository
(`promote.md`), which may be copied by hand or sent as a pull request.

A **workspace** is the unit that moves between servers
([0025](0025-workspace-memory-single-writer.md)). A move has four steps: the
workspace is frozen on the old server (no new runs, claims released), a last
copy of its memory repository is made, the new server marks it active, and the
old server removes it, including its `state/` and its queue. Work item ids stay
the same across servers ([0023](0023-tracker-independent-work-item.md)), so the
history of a work item survives a move. A repository is never active on two
servers.

## Consequences

- The personal and enterprise settings cannot leak into each other by
  configuration, since they share no machine, token, database or network.
- A new server is created by the same code as the first one
  ([0028](0028-deployment-is-code-in-two-layers.md)), with its own variables.
- A repository can change owner of its running copy without a hand copy, as long
  as the workspace is the thing that moves.
- Two servers cost two machines to maintain, update and back up, and two sets of
  credentials to protect.
- Nothing shows one view of everything. A device sees one server at a time, so a
  cross-server overview is a separate piece of work.
- Rules that apply to both settings, such as shared lessons, exist twice and can
  drift until someone copies them.
- Enterprise mode depends on the client's or employer's way in, which this
  project does not control.

## Alternatives

- **One server for everything, separated by tags and access rules.** It is one
  machine to run. A mistake in a rule or a token then crosses the boundary, and a
  personal workload sits on a machine in the company's account.
- **One control plane over several servers.** It gives one view, and it
  centralizes data that must stay separate, and it makes the enterprise side
  depend on a personal machine, or the reverse.
- **Two tailnets on one device.** It does not work with the client as shipped:
  one is active at a time, and the addresses can collide. Workarounds with
  several daemons or a relay exist and are fragile.
- **Replicate state between servers.** It saves the move steps and breaks the
  single-writer rule of 0025, and it copies data across the boundary.

## Open questions

- How a colleague reaches the enterprise server: share one machine from a
  tailnet, a company tailnet, or a bastion. The first is not verified.
- Whether enterprise mode signs colleagues in with the employer's identity
  server (OpenID Connect, as Keycloak offers) instead of a network gate, and what
  the dashboard and `opencode serve` would need to accept it.
- Where the context definitions live on a device, and whether they carry
  secrets or only references to them.
- How the freeze handles a pull request that is open during a move, and a claim
  ([0026](0026-claims-and-presence.md)) that is still held.
- Whether the current server, which holds some of the owner's work for now,
  becomes the first enterprise server or is retired after the move.
- Which repositories are personal and which are enterprise, and who decides when
  a project is both.
