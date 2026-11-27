# 0035. A dot is an instance of the engine with a policy profile

- Status: accepted
- Date: 2026-11-26
- Supersedes: none
- Superseded by: none

## Context

The first framing of this record asked whether a dot serves one person or a team. Working it
through showed the question was wrong. A dot is not a person's assistant and not a chat
partner. It is a **control plane**: it takes in work items from sources
([0023](0023-tracker-independent-work-item.md)), adds judgement, and runs fixed `awf`
workflows. Side effects stay in shell steps ([0002](0002-shell-steps-own-side-effects.md)). The
dashboard tracks the work and holds the intelligence.

What differs between two dots is not their shape but their **policy**:

- the owner's own dot maintains repositories that are largely written with an agent, and keeps
  packages up to date. It may own the code outright;
- a company's dot runs on a private network, on code that belongs to others, and must be less
  autonomous, with a trace of every decision.

Both are wanted at once, by the same person. A workstation that wants to work locally is a
worker that joins an instance ([0033](0033-hybrid-executors.md)).

Whether any of this is sold is not decided. Today it is a passion project that lightens package
maintenance, and it is open source. Later it could be offered as a service, with infrastructure
and integrations around the engine. Nothing here depends on that choice, and nothing here
should make it harder.

## Decision

We define a **dot** as one instance of this engine running one **policy profile**. A profile
sets the defaults for autonomy ([0040](0040-classification-and-autonomy-ceiling.md)), trust
([0032](0032-trust-levels-per-repository.md)), identity
([0034](0034-dot-identity-in-github.md)), budget
([0036](0036-model-budgets-and-caps.md)) and roles
([0037](0037-dashboard-users-and-roles.md)). The same code serves a personal profile and a
company profile.

A person may use several dots. Two instances at least exist: the personal one and the
company's. Each repository has exactly one instance that acts on it; any other is subject to
[0038](0038-what-two-dots-share.md). Instances stay independent
([0029](0029-independent-servers-and-contexts.md)).

We keep the engine modular on three seams, so a boundary between what is public and what is
offered can be drawn later without a rewrite: the **sources** (adapters,
[0039](0039-work-sources-are-adapters.md)), the **runner** that executes untrusted steps, and
the **workflows**. We do not decide that boundary here.

### First step built

`repos.example.yaml` now ships two named profiles, `personal` and `company`, and one example repository for each.
They run the same workflows and differ only by policy: `personal` sets `autonomy: auto`, the circuit breaker and
`tend.auto_merge`; `company` sets `trust: client`, `autonomy: act`, `identity: marker` and a daily `budget`. A test
(`internal/repos/features_profiles_test.go`) generates the example and checks that each profile reaches its rules
and `tend.list` with its own policy, and that a company profile asking for `auto` is refused by the trust rule.
The profile keys already carried by the engine are autonomy, trust, identity, budget, `dot`, `upstream`,
`circuit_breaker` and `advisories`. No selector of instances on a device exists yet.

## Consequences

Easier: the personal and company cases are one code base with two configurations. The cases C1
to C5 become two instances plus a rule on shared repositories, not a new kind of product.

Harder: every default must be a property of a profile and not a constant in the code, and the
dashboard must show which profile an instance runs.

Rules out: building a team product first. The company profile is the same engine under tighter
policy.

## Alternatives

One dot per person, one per team, as the first draft: both are profiles of this, not
architectures. Two products with two code bases: doubles every fix and loses the personal dot's
use as the place where the company's workflows are tried first.
