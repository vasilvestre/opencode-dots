# 0050. From client up, the code stays in Europe and only zero-retention models read it

- Status: proposed
- Date: 2026-11-27
- Supersedes: none
- Superseded by: none

Extends [0032](0032-trust-levels-per-repository.md), which is not rewritten.

## Context

0032 gave a `trust` key with three levels and allow-lists of providers, models and executors. It did not say
**why** a model or a machine is acceptable. The owner's answer is two questions that decide it:

- does the provider keep what it reads? Zero data retention (ZDR) is what matters for another party's code;
- where does the code go? A machine and a model provider in Europe, at least.

The level `strict` of 0032 also meant "kept on a designated server, never on a laptop", which is not how
the owner uses the word. What the owner has needed so far is a level for models the owner hosts, and a strict
level that may keep no local checkout, although no such case exists yet.

## Decision

We add a level and two rules to `trust`, and make the allow-lists carry their reasons.

**Four levels**, from open to closed: `open`, `client`, `private`, `strict`. A machine's `host` level is the
highest it may hold, as before.

**Europe, from client up.** `settings.trust.region` says where this machine runs, and
`settings.trust.regions` says where each named executor runs. A repository at `client`, `private` or `strict`
needs both the machine and its executor in Europe: a region that starts with `eu`. A region that is missing counts
as not in Europe.

**Zero retention, from client up.** `settings.trust.zdr` lists the model providers the owner has a zero
retention guarantee from. A provider is the prefix of a model id before the slash (`mistral/small` is `mistral`), or
`anthropic` for a bare model name given to the `claude` CLI. A model set on a rule, and the `claude` provider
itself, must come from a provider in `zdr` (or in `self_hosted`). The owner keeps the list true: the generator
cannot check a contract.

**Self-hosted, for private and strict.** `settings.trust.self_hosted` lists the model providers the owner runs.
They count as zero retention, and they are all a `private` or `strict` repository may use. `claude` never
qualifies. This is the level between `client` and `strict` that the owner asked for: the code reaches only
models on machines the owner controls.

**Strict may keep no local checkout.** As in 0032, a `strict` repository refuses a `checkout` on a workstation.
The owner has no such repository today, so the rule stays as written and costs nothing until one exists.

`bin/repos generate` refuses a block that breaks a rule and names the key. A level is raised by an edit of
`repos.yaml` and is never lowered by a default.

## Consequences

- The reason a model is allowed is written in the configuration: a reader sees which providers are ZDR and which
  are hosted, instead of a bare list of ids.
- A repository at `client` or above cannot be generated until the machine and its executor have a region, and the
  model providers it uses are declared. The example file and the tests carry them.
- The check is of the configuration, as in 0032: it does not see a model chosen inside a workflow default, a
  contract that lapses, or where a provider really stores data. It is a written statement the owner answers for.
- `private` is new: no existing repository uses it, so nothing changes for the repositories that were `client`
  or `open`, apart from the new requirements on `client`.

## Alternatives

- **Levels defined by place only (local, designated server).** What 0032 started with; it says where the code
  sits but not what reads it, which is the owner's actual concern.
- **A built-in list of ZDR providers.** It would go stale and put a claim about third parties in our code. The owner
  names the providers and answers for them.
- **Per-model ZDR instead of per provider.** More precise, and more to maintain; a provider guarantee is a contract
  with the provider as a whole.
