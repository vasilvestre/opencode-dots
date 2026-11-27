# opencode-dots

A control plane that keeps your repositories maintained by AI agents, and keeps the agents on a short
leash. It watches GitHub (and, as adapters land, advisories, Sentry and ticket trackers), turns what it
finds into work items, and runs fixed [awf](https://github.com/awf-project/cli) workflows that review pull
requests, answer review comments, fix issues and failing checks, and tend a repository over time.

It is not a chat agent. The agent writes text; **shell steps own every side effect**, and every outward
write goes through one gate that knows how much autonomy the repository has been given.

```
 GitHub ──► awf-gh (watcher) ──► queue ──► worker ──► awf workflow ──► agent (writes text only)
                                   ▲          │                              │
        advisories, Sentry,        │          │                              ▼
        tickets (adapters) ────────┘          └────────────► shell steps ──► publish gate ──► GitHub
                                                                                (autonomy, trust, budget,
 dashboard (activity, cost, links, audit, pause)  ◄─── state, logs, control-plane database           pause, patch guard)
```

## What it does

| Area | What you get | Read |
| --- | --- | --- |
| Workflows | `review-pr`, `reply-pr`, `fix-issue`, `fix-ci`, `tend-repo`, and helpers; each is a state machine you can read | [workflows](docs/workflows.md) |
| Autonomy | One ceiling per repository: `observe` (a dry run), `propose` (held for a person), `act` (comments and pull requests), `auto` (merges its own) | [autonomy](docs/autonomy.md) |
| Safety | A publish gate, an emergency stop, a circuit breaker, a daily spend cap, a patch guard and container tests for untrusted code | [pause](docs/pause.md), [breaker](docs/breaker.md), [budget](docs/budget.md), [runner](docs/runner.md) |
| Trust | `open`, `client`, `private`, `strict`: Europe and zero-retention models from `client` up, hosted models for `private` and `strict` | [ADR 0032](ADR/0032-trust-levels-per-repository.md), [ADR 0050](ADR/0050-trust-needs-europe-and-zero-retention.md) |
| Teams | Workers pull jobs from a queue, a dashboard sign-in with GitHub, SSH access without a VPN, identity marking | [remote](docs/remote.md), [team UI](docs/team-ui.md), [identity](docs/identity.md) |
| Memory | Lessons a repository teaches the dot, kept in one writer's workspace | [memory](docs/memory.md), [workspace](docs/workspace.md), [tend](docs/tend.md) |
| Sources | Work items from advisories, Sentry and trackers, classified before anything runs | [sources](docs/sources.md), [classification](docs/classification.md) |
| Upstream | The dot can file issues on the tools it depends on, with evidence and a weekly cap | [upstream](docs/upstream.md) |

Everything beyond the base watcher is **opt-in and off by default**: a repository with no extra key behaves
as it did before the key existed. The full map of use cases and what is built is in
[docs/use-cases.md](docs/use-cases.md); what still needs a decision is in
[docs/decisions-to-take.md](docs/decisions-to-take.md).

## Principles

- **The agent never commits, pushes, posts or merges.** A shell step does, after the gate agrees.
- **Text from outside is data**: pull request bodies, review comments, issue text and diffs are never
  instructions.
- **One list of repositories** (`repos.yaml`) decides what the dot does on each one; the files the tools read
  are generated from it. Private names never enter git.
- **Everything is a file you can read**: workflows in `workflows/`, state under `STATE_DIR`, decisions in `ADR/`.

## Machines and roles

A machine has one of three roles, installed from one manifest ([units](docs/units.md)):

| Role | Runs | Typical machine |
| --- | --- | --- |
| `workstation` | `awf`, the dashboard, the watcher, the short recurring jobs (`bin/tick`), optionally a worker | your own computer: a personal dot |
| `server` | the same, plus the Mercure hub and the job queue | a small always-on server: a team's dot |
| `worker` | a local `awf`, `dots-worker` | a developer's laptop that runs the server's jobs |

A server watches GitHub and queues the work. A worker (any machine with an `awf` and an agent CLI) claims a job,
runs it on its own `awf`, and reports the result and its cost; it needs one outbound HTTPS connection and no
tunnel ([ADR 0048](ADR/0048-workers-pull-jobs-from-a-queue.md)). You can be both at once: your workstation is your
own dot and a worker for a team's server.

## Get started

Prerequisites: `git`, `gh` (logged in), `jq`, Go (to build), `awf` and an agent CLI (`opencode`, or `claude`).

```bash
git clone <this repository> && cd opencode-dots
make hooks                              # once per clone: refuses a commit that adds a binary
make go-build                           # bin/dashboard, bin/repos, bin/health, bin/worker, bin/dots
cp repos.example.yaml repos.yaml        # edit: which repositories, and what the dot does on each
make repos                              # generates awf-gh.yaml, tend.list, repos.map, ... from repos.yaml
bin/dots-units install --role workstation --prune
bin/dots-units status                   # what is missing, off, extra or obsolete
```

The dashboard is on <http://127.0.0.1:8099>, awf on `127.0.0.1:2511`. Nothing binds a public interface.
`bin/tick list` shows the recurring jobs and how each last ended. Start a repository at `autonomy: observe`
([autonomy](docs/autonomy.md)) to see what the dot would do before it may act.

- **A server for a team:** [docs/remote.md](docs/remote.md) (the queue, the hub, worker tokens, one public
  entry) and [docs/team-ui.md](docs/team-ui.md) (the dashboard over SSH). The server is installed from
  `deploy/` ([ADR 0028](ADR/0028-deployment-is-code-in-two-layers.md)).
- **A worker for a team's server:** put the token in `~/.config/dots/worker-token`, `DOTS_WORKER_URL` in
  `~/.config/dots/worker.env`, then `bin/dots-units install --role workstation` (or `worker`).
- **Where your projects live on the machine:** [docs/paths.md](docs/paths.md) (`~/.config/dots/paths`, the card
  on the Home page).

## Operating it

| To | Do |
| --- | --- |
| See what happened | the dashboard: Home, Activity, Cost, Audit, Links, Health ([dashboard](docs/dashboard.md)) |
| Stop everything, or one repository | `bin/pause all`, `bin/pause repo OWNER/REPO`, or the Stop buttons on Home |
| Is it alive | `make health`, the Health page, `bin/tick list` |
| Release what a person must approve | the Release button, or [publish-held](docs/publish-held.md) |
| Back up a server | `bin/backup-server user@host` ([backup](docs/backup.md)) |
| Read a run that went wrong | `bin/dots` ([debug](docs/debug.md)) |

## Developing

```bash
make ci            # every step of the CI, locally: run it before pushing, then read the CI result
make validate      # parse the workflows
bin/test-e2e       # the workflows end to end, offline (fake gh, git and agent)
make adr           # ADR numbering, index and supersede links
```

`make ci` runs `go vet`, the Go tests, the shell test suites and `deslop-please`. The layout and the rules for
editing are in [AGENTS.md](AGENTS.md). Decisions are recorded in [ADR/](ADR/README.md): an accepted record is
never rewritten, a new one supersedes it and both files name the other.

## Documentation

[docs/README.md](docs/README.md) indexes every document by what you want to do. The older Docker-based setup is
kept in [docs/docker.md](docs/docker.md); it is not the supported install.
