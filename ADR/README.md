# Architecture decision records

One file per decision that shaped this project. A record states what we chose,
why, and what it costs. It is history: never rewrite an accepted record. Change
your mind by writing a new one that supersedes it.

## Index

| Number | Title | Status | Date |
| --- | --- | --- | --- |
| [0001](0001-memory-split-in-three.md) | Split agent memory into three stores | accepted | 2026-11-23 |
| [0002](0002-shell-steps-own-side-effects.md) | The agent never writes to a remote; shell steps do | accepted | 2026-11-23 |
| [0003](0003-private-config-not-in-git.md) | Private names never enter git | accepted | 2026-11-23 |
| [0004](0004-dashboard-edits-local-config.md) | The dashboard edits local config files | superseded by 0017 | 2026-11-23 |
| [0005](0005-run-cost-from-opencode-db.md) | Run cost comes from opencode's database | superseded by 0008 | 2026-11-23 |
| [0006](0006-per-pull-request-memory.md) | Memory of one pull request across runs | accepted | 2026-11-23 |
| [0007](0007-ask-the-agent-about-a-pull-request.md) | Ask the agent about a pull request from the dashboard | accepted | 2026-11-23 |
| [0008](0008-run-cost-comes-from-awf.md) | Run cost comes from awf | accepted | 2026-11-23 |
| [0009](0009-review-pr-merges-fix-pulls.md) | review-pr merges its own fix-issue pull requests | accepted | 2026-11-23 |
| [0010](0010-review-fix-loop.md) | review-pr closes its own loop: fix, push, re-review | accepted | 2026-11-23 |
| [0011](0011-sync-managed-clones.md) | Sync managed clones when main moves | accepted | 2026-11-23 |
| [0012](0012-fix-rule-judges-decisions.md) | The fix rule judges decisions, not line counts | accepted | 2026-11-23 |
| [0013](0013-dashboard-on-the-tailnet.md) | The dashboard answers on the tailnet through tailscale serve | accepted | 2026-11-23 |
| [0014](0014-dashboard-editable-model-defaults.md) | The dashboard edits workflow model defaults | superseded by 0018 | 2026-11-24 |
| [0015](0015-dots-take-delegation.md) | The operator delegates a pull request with /dots take | accepted | 2026-11-25 |
| [0016](0016-awf-gh-runs-natively.md) | awf-gh can run natively | accepted | 2026-11-25 |
| [0017](0017-dashboard-edits-completion-notifications.md) | The dashboard edits completion notifications | accepted | 2026-11-25 |
| [0018](0018-dashboard-managed-model-presets.md) | The dashboard manages global model presets | superseded by 0019 | 2026-11-25 |
| [0019](0019-claude-model-presets.md) | Model presets are per provider | accepted | 2026-11-25 |
| [0020](0020-fix-issue-works-from-a-fork.md) | fix-issue works from a fork when it cannot push upstream | accepted | 2026-11-25 |
| [0021](0021-fix-issue-routes-to-hidden-workflows.md) | fix-issue routes to hidden workflows | accepted | 2026-11-25 |
| [0022](0022-repos-yaml-is-the-source-of-truth.md) | repos.yaml is the source of truth for the repository list | superseded by 0024 | 2026-11-25 |
| [0023](0023-tracker-independent-work-item.md) | A work item is independent of the tracker | accepted | 2026-11-26 |
| [0024](0024-control-plane-database.md) | A database owned by dots is the source of truth for the control plane | accepted | 2026-11-26 |
| [0025](0025-workspace-memory-single-writer.md) | Workspace memory lives in a private repository with one writer | accepted | 2026-11-26 |
| [0026](0026-claims-and-presence.md) | A claim says a human works on a work item | accepted | 2026-11-26 |
| [0027](0027-eu-vps-native-local-mode.md) | The dot runs on one EU VPS in native local mode | accepted | 2026-11-26 |
| [0028](0028-deployment-is-code-in-two-layers.md) | Deployment is code, in two layers | accepted | 2026-11-26 |
| [0029](0029-independent-servers-and-contexts.md) | Servers are independent and each device picks a context | proposed | 2026-11-26 |
| [0030](0030-watcher-is-opt-in.md) | The GitHub watcher is opt-in | accepted | 2026-11-26 |
| [0031](0031-executor-per-repository.md) | A repository names the executor that runs its tasks | accepted | 2026-11-26 |
| [0032](0032-trust-levels-per-repository.md) | A repository has a trust level that limits the rest of its configuration | accepted | 2026-11-26 |
| [0033](0033-hybrid-executors.md) | An executor can be a list, and what every executor runs is checked | proposed | 2026-11-26 |
| [0034](0034-dot-identity-in-github.md) | The dot says who it is in GitHub, and the identity is a per-repository choice | accepted | 2026-11-26 |
| [0035](0035-a-dot-is-an-engine-instance-with-a-policy-profile.md) | A dot is an instance of the engine with a policy profile | accepted | 2026-11-26 |
| [0036](0036-model-budgets-and-caps.md) | Model spending has a payer, a budget and a hard cap | proposed | 2026-11-26 |
| [0037](0037-dashboard-users-and-roles.md) | The dashboard has users, roles and an audit trail | accepted | 2026-11-26 |
| [0038](0038-what-two-dots-share.md) | Two dots on one repository share metadata and reviewed lessons, never code or raw memory | superseded by 0045 | 2026-11-26 |
| [0039](0039-work-sources-are-adapters.md) | Work sources are adapters that feed work items | accepted | 2026-11-26 |
| [0040](0040-classification-and-autonomy-ceiling.md) | Each item is classified, each repository has an autonomy ceiling, the action is the lower of the two | accepted | 2026-11-26 |
| [0041](0041-linking-work-items-is-a-proposal.md) | Linking work items across sources is a proposal; merging stays explicit | accepted | 2026-11-26 |
| [0042](0042-workstations-are-workers-of-an-instance.md) | A workstation is a worker that joins an instance | accepted | 2026-11-26 |
| [0043](0043-dot-files-issues-on-named-upstream-repositories.md) | The dot may file issues on named upstream repositories | proposed | 2026-11-26 |
| [0044](0044-dashboard-shows-only-the-repositories-a-viewer-can-reach.md) | The dashboard shows a viewer only the repositories GitHub says they can read | proposed | 2026-11-27 |
| [0045](0045-dots-do-not-talk-to-each-other.md) | Dots do not talk to each other; the owner keeps a repository on one dot | accepted | 2026-11-27 |

## When to write one

Write a record before the code when any of these is true:

- the change needs a choice between two reasonable designs;
- an issue needs more than about forty lines, a new dependency, or a new
  interface;
- the change touches memory, state layout, security, or what the agent may do
  on its own;
- you expect someone to ask "why is it like this?" in six months.

A bug fix that only restores intended behaviour needs no record.

## Lifecycle

`proposed` → `accepted` → `superseded by NNNN`.

Keep `proposed` only while the discussion is open. A record that nobody acts on
becomes `rejected` with the reason in one line.

To supersede, write the new record and set both fields:

- the new file gets `Supersedes: NNNN`;
- the old file gets `Superseded by: NNNN` in its status and in its header. Do
  not edit anything else in the old file.

## Commands

```bash
bin/adr new drop-docker-mode   # create ADR/0004-drop-docker-mode.md
bin/adr check                  # numbering, one index row per record (title, status, date), supersede links
bin/adr proposed               # the records still waiting for a decision
bin/adr accept 0023 0025       # proposed -> accepted in the record and the index; a record that supersedes
                               # another one also marks the old one "superseded by"
```

`bin/adr check` runs in `make adr` and in `make validate`.

## Issue too large

When an issue needs a design decision, the fix workflow does not guess. The
agent writes a draft record into its answer, and the draft is posted as a
comment on the issue. To turn it into a record:

```bash
bin/adr new <slug>
# paste the draft under Context and Decision, then set Status: proposed
```

The record is accepted when the maintainer says so in the issue.
