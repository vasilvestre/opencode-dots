# Workers

ADR 0042, first step: presence only. A worker is a machine that tells this instance it exists and
keeps saying so. The instance sends it nothing: there is no task pulling, no protocol and no
authentication yet, so a worker listed here cannot do anything.

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

## Not done

Pulling tasks, the executor allow-list of ADR 0032, a token or tailnet identity for a worker, and
cost visibility (ADR 0036). See ADR 0042.
