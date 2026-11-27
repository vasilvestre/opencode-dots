# Executors: watch on one machine, run on another

ADR 0031 lets a server watch GitHub around the clock while the tasks run on another machine, with its own agent CLI
and its own GitHub identity. A repository names its **executor** with the `executor` key; the watcher (`awf-gh`)
stays on the server and posts each event to that executor.

**How an executor receives its tasks has changed.** The first design pushed each event to the executor's `awf serve`
through a reverse SSH tunnel. [ADR 0048](../ADR/0048-workers-pull-jobs-from-a-queue.md) replaced it: the executor's
address is now the dashboard's job queue, and workers **pull** jobs from it with a revocable token, over one outbound
HTTPS connection. See [remote.md](remote.md) for the queue, the hub and the worker, and [units.md](units.md) for the
`worker` role.

## Set it up

On the server, in `repos.yaml`:

```yaml
settings:
  executors:
    workstation: http://127.0.0.1:8099/queue     # the job queue of this server's dashboard
repos:
  your-org/your-repo:
    review: true
    executor: workstation
```

`bin/repos generate` writes `awf-gh.workstation.yaml` (and no `awf-gh.yaml` if no repository runs on the server
itself). The `awf-gh` task of `bin/tick` ([units.md](units.md)) polls every watcher file it finds. `tend` runs where
`bin/tend` is scheduled. Then issue a worker token and start a worker on the machine that runs the tasks:
[remote.md](remote.md).

## Behaviour

- A repository at `trust: client` or above must name an executor from `settings.trust.allow.executors`, and an
  executor in Europe (`settings.trust.regions`): its code has to stay on a machine you named for it
  ([ADR 0032](../ADR/0032-trust-levels-per-repository.md), [ADR 0050](../ADR/0050-trust-needs-europe-and-zero-retention.md)).
- A job nobody has claimed waits in the queue (24 hours), and a worker that is off delays a job and never loses it.
- The server's `GH_TOKEN` only has to read pull requests and issues. The steps that comment, push and open pull
  requests run on the worker's machine with its token.
- `awf-gh` refuses to start without its seen file, so the first pass never dispatches the backlog by accident. Run
  `bin/awf-gh --config awf-gh.<executor>.yaml --seed` to mark the open pull requests as seen.

## The earlier push model

`make executor-tunnel SERVER=user@host` and `systemd/dots-executor-tunnel.service` still open the reverse tunnel, for
an executor configured with the tunnel's address (`http://127.0.0.1:2512`) as before. `bin/dots-units prune` removes
the tunnel unit from a machine, since the queue makes it unnecessary.
