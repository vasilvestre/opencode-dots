# 0032. A repository has a trust level that limits the rest of its configuration

- Status: proposed
- Date: 2026-11-26
- Supersedes: none
- Superseded by: none

## Context

[0029](0029-independent-servers-and-contexts.md) separates the owner's personal work from
the employer's and the clients' work by giving each its own server. That is the strong
boundary. Inside one server the repositories still differ: a public plugin and a client
project do not deserve the same treatment, and nothing says so today.

What the dot does on a repository is one block of keys in `repos.yaml`
([0022](0022-repos-yaml-is-the-source-of-truth.md), [0024](0024-control-plane-database.md)):
the model or agent CLI (`provider`, the presets), the executor
([0031](0031-executor-per-repository.md)), a local `checkout`, `tend` with `auto_merge`.
Each of these can send code to a model provider, keep a copy of it on a machine, or merge
without a person. A repository that must not do one of them has no way to forbid it, so the
only guard is the owner remembering.

The machines are small and ordinary: no `/dev/kvm`, so no microVM sandbox, and a bare metal
server that would give one costs several times the VPS
([0027](0027-eu-vps-native-local-mode.md)). The employer's own agent platform shows a
cheaper pattern: model output is applied by a worker that holds the token, and tests of
untrusted code run in a throwaway container with no network, a read-only root and no
capabilities. That pattern needs a runner that the dot does not have yet.

## Decision

We add a `trust` key under a repository, with three levels, and we let the generator refuse
a block that contradicts its level.

- **`open`** (the default): public or personal code. Any model, any executor, `auto_merge`
  allowed.
- **`client`**: another party's code. The models must come from an allow-list named in
  `settings`, `auto_merge` is refused, and the executor must be one that keeps the code on
  machines the owner named for that purpose.
- **`strict`**: code that must not leave a given place. As `client`, plus no `checkout`
  kept on a workstation, and the repository is only accepted on a server whose settings say
  it is a strict server.

`bin/repos generate` checks these rules before it writes anything, as it already checks
events and executor names, and says which key breaks which level. A level can only be
raised by one `repos.yaml` edit and is never lowered by a default: a default supplies
values and never switches a feature on ([0022](0022-repos-yaml-is-the-source-of-truth.md)),
and `trust` follows it.

The check is of the configuration. It does not stop a running agent from reading what it can
read, and we say so: the isolation that holds today is the machine boundary of 0029. A
runtime sandbox for the steps that run code of others is a later record, and it will use the
runner pattern above, with `gVisor` or rootless containers where there is no `/dev/kvm`.

## Consequences

- A client repository cannot be sent to an unlisted model or merged by the dot by mistake: the
  generator refuses it, before any run.
- The level is visible in `repos.yaml` and in the dashboard, so a review of what is allowed is
  one list.
- The rules are only as good as the allow-list and the list of strict servers, which the owner
  keeps. A wrong entry is a silent hole.
- An agent that runs on the owner's machine as an executor still has that machine's access.
  `trust` does not sandbox it.
- Every new key that can move code (a new executor kind, a new provider) must say what it does
  at each level, or the check is incomplete. That is one more rule to remember when adding a
  feature.

## Alternatives

- **Rely on the machine boundary only.** It is the strongest guard and costs nothing here, but
  it cannot tell two repositories on the same server apart, and it leaves the choice of model to
  habit.
- **Sandbox every step.** The right end state for untrusted code, and it needs a runner and a
  runtime the machines do not have. It is a separate, larger decision.
- **Tags on repositories with rules in the tailnet access list.** It guards network reach, not
  which model sees the code or whether a merge happens.
- **Four or more levels.** More precision, and a table nobody keeps in their head. Three can be
  told apart by what each forbids.

## Open questions

- What the model allow-list holds for the first client, and who may change it.
- How the dot knows a server is strict, and how that is shown to a colleague.
- Which runtime sandbox, once one is needed: rootless containers, `gVisor`, or a microVM on
  bare metal.
- How audit works: what is logged per run, for how long, and who may read it.
- How a repository changes level, and what happens to the memory it built at the old one
  ([0025](0025-workspace-memory-single-writer.md)).
