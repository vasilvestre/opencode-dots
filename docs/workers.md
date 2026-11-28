# Workers

ADR 0042, first step: presence only. This page is about the **presence registry**: a machine tells this instance
it exists and keeps saying so, and the dashboard's Workers card lists what the registry holds.

Workers that **run jobs** are a different, later piece: they pull from the job queue with a revocable token and
report results ([ADR 0048](../ADR/0048-workers-pull-jobs-from-a-queue.md), [remote.md](remote.md)). They do not appear
in this registry: the queue knows them by their token's label (`bin/worker-token list`, `GET /api/queue`).

```bash
bin/repos db worker register  -name ada-laptop -machine ada-laptop -capabilities '{"models":["local"]}' [-ttl 90s]
bin/repos db worker heartbeat -name ada-laptop [-ttl 90s]   # every 30 s or so; fails once expired
bin/repos db worker list                                    # name, machine, live or expired, expiry, capabilities
bin/repos db worker expire    -name ada-laptop              # end it now
```

- A worker is a row of the `worker` table and a `bot` actor (identity `worker:<name>`). Registering
  twice is one worker: it is renewed, and a live one keeps its registration time.
- The time to live is the claim one: 90 s by default, at most one hour. Every time is this server's
  clock; a worker never sends one.
- A heartbeat of an expired worker is refused ("register again"): it is not revived silently.
- `-capabilities` is a JSON object, kept as given (keys are not interpreted yet).
- Names have no spaces. An expired worker stays listed, as expired, until it registers again.

## Dashboard

`GET /api/workers` (read only, trusted host only) lists every worker, live first, with `live`,
`registered_at`, `renewed_at` and `expires_at`. The Repos view shows a Workers card with the live
and expired counts and each machine's age. The card is absent while no worker has registered.

## Which agent a worker runs

A worker says which agent CLIs it runs: `dots-worker -agents claude,opencode` (or `DOTS_AGENTS`), by default
whatever it finds on `PATH`, opencode first. It sends the list with every request (`X-Worker-Agents`).
The queue then offers it only the jobs it can run: a job runs on its `provider` input, on opencode when it
has none, and a worker that declares nothing is not filtered (an older worker keeps working).

A repository can leave the choice to the worker that takes the job: `provider: any`. It needs an
`executor`, since the choice is made at the queue (so it is set on a repository, never as the default for all of them), and the worker fills in the first agent it runs. A
colleague who uses only claude and one who uses only opencode can then serve the same repository.
Health lists the workers reading the queue with their agents (`GET /api/queue/workers`, owner only,
kept in memory).

## Not done

Pulling tasks, the executor allow-list of ADR 0032, a token or tailnet identity for a worker, and
cost visibility (ADR 0036). See ADR 0042.
