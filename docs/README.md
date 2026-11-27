# Documentation

Start with the [README](../README.md) for what the project is and how to install it. This page finds the rest by
what you want to do. Decisions live in [ADR/](../ADR/README.md); a document here describes what is built and says
what is not.

## I want to understand it

| Document | What it says |
| --- | --- |
| [use-cases.md](use-cases.md) | Every use case (one person, a team, several dots, a company), and the state of each |
| [decisions-to-take.md](decisions-to-take.md) | What is decided, what is open, what only the owner can do |
| [workflows.md](workflows.md) | Each workflow: trigger, inputs, steps, a skeleton to write your own |
| [store.md](store.md) | The control-plane database: work items, claims, links, audit, workers |
| [memory.md](memory.md) | Why the memory is split in three, with the measurements |

## I want to set a machine up

| Document | What it says |
| --- | --- |
| [units.md](units.md) | The three roles, `bin/dots-units`, and `bin/tick` (one timer for every short recurring job) |
| [repos.md](repos.md) | `repos.yaml`: which repositories, and what the dot does on each |
| [paths.md](paths.md) | Where your projects live on the machine, the team's folder suggestion, the Projects card |
| [clones.md](clones.md) | Where the dot keeps its own clones |
| [agent.md](agent.md), [skills.md](skills.md), [tower.md](tower.md) | The agent configuration, skills, and the memory MCP |
| [notifications.md](notifications.md) | Discord messages |
| [docker.md](docker.md) | The older Docker setup (not the supported install) |

## I want to run it for a team

| Document | What it says |
| --- | --- |
| [remote.md](remote.md) | A server, the job queue, the Mercure hub, worker tokens and the one public entry |
| [workers.md](workers.md) | The workers a server knows, and how they are listed |
| [team-ui.md](team-ui.md) | The dashboard for developers over an SSH forward, without a VPN |
| [dashboard-auth.md](dashboard-auth.md) | Sign-in with GitHub, and what a visitor may see |
| [colleagues.md](colleagues.md) | What the dot looks like to a colleague on a shared repository |
| [pickup.md](pickup.md) | The label that says a dot took a pull request |
| [claims.md](claims.md) | Claims: a person working on a branch, and the events around it |
| [dots.md](dots.md) | Two dots on one repository, by configuration |
| [identity.md](identity.md) | Marking what the dot writes, or not |
| [audit.md](audit.md) | The audit of dashboard writes |

## I want to keep it safe

| Document | What it says |
| --- | --- |
| [autonomy.md](autonomy.md), [autonomy-view.md](autonomy-view.md) | The ceiling per repository, the publish gate, notes and held items |
| [pause.md](pause.md) | The emergency stop, and `autonomy: observe` as the dry run |
| [breaker.md](breaker.md) | The circuit breaker after a bad merge |
| [budget.md](budget.md) | The daily spend cap |
| [runner.md](runner.md) | The patch guard and container tests for untrusted code |
| [classification.md](classification.md) | Classifying a work item before anything runs |
| [publish-held.md](publish-held.md) | Releasing what a person must approve |
| [upstream.md](upstream.md) | Reporting to upstream repositories, with evidence and a cap |
| [review-charter.md](review-charter.md) | What a review must check |

## I want to feed it work

| Document | What it says |
| --- | --- |
| [sources.md](sources.md) | Work sources are adapters that produce work items |
| [sources-sentry.md](sources-sentry.md), [sources-tracker.md](sources-tracker.md), [advisories.md](advisories.md) | The Sentry, tracker and advisory adapters |
| [links.md](links.md) | Link proposals between work items, confirmed by a person |
| [tend.md](tend.md) | The daily pass that learns from what shipped |
| [workspace.md](workspace.md) | The lessons a repository teaches, kept in one writer's workspace |

## I want to look after it

| Document | What it says |
| --- | --- |
| [dashboard.md](dashboard.md) | The pages of the dashboard |
| [health.md](health.md) | Is the dot alive, and the alert |
| [backup.md](backup.md) | Backing up a server, workstation or server |
| [debug.md](debug.md) | Reading a run that went wrong |
| [executors.md](executors.md) | The earlier push model of an executor, replaced by the queue |

## I want to work on the code

| Document | What it says |
| --- | --- |
| [e2e.md](e2e.md) | The offline end-to-end tests of the workflows |
| [../AGENTS.md](../AGENTS.md) | The layout, the rules for editing, the anti-patterns |
| [../ADR/README.md](../ADR/README.md) | The decision records and when to write one |
