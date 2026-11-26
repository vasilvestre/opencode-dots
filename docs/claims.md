# Claims and events

ADR 0026. A claim says a human works on a work item. The bot never pushes to a branch that
carries an active claim held by a human; it waits. A claim expires by itself when its heartbeats
stop, and every time in it is the server's clock.

```bash
bin/repos db claim -ref github:owner/repo#14 -actor ada -machine laptop [-branch topic] [-ttl 90s]
bin/repos db heartbeat -claim cl_...      # every 30 s or so; fails when the claim already expired
bin/repos db release -claim cl_...
bin/repos db claims -work-item wi_...     # who holds it now
bin/repos db claimed -repo owner/repo -branch topic   # for a shell step: exit 0 free, 10 held by a human
```

A shell step that is about to push asks `claimed` first:

```bash
bin/repos db claimed -repo "$repo" -branch "$branch" || { [ $? -eq 10 ] && echo "branch is claimed: waiting"; exit 0; }
```

- The time to live defaults to 90 s and is capped at one hour. A late heartbeat is refused, so the
  agent claims again knowing the bot may have worked in between.
- A claim on a work item covers its `branch:` reference; a claim that names a branch covers that
  branch of the work item's repository only.
- A bot's own claim never vetoes the bot. A second human claim is allowed and prints a warning.
- Asking twice from one machine is one claim, renewed.

## The bus

`DOTS_MERCURE_URL` and `DOTS_MERCURE_SECRET` turn on notifications. `claim`, `heartbeat` and `release`
then publish a private update on the topic `dots/claim/<work item id>`. It is best effort: the claim
is in the database, and a hub that is down loses a notification, not a claim.

```bash
bin/repos events subscribe dots/claim/wi_...    # one JSON payload per line, resumes after a drop
bin/repos events publish TOPIC DATA
```

`internal/events` speaks the Mercure protocol directly (a signed publish form, a server-sent-event
subscription that resumes with `Last-Event-ID`). It was tested against a fake hub. No hub is deployed:
that is the owner's decision (the hub is AGPL-3.0, and sources disagree on a licence key for
self-hosting; check before deploying).

Not done: the local agent that renews a claim by itself, the dashboard list of active claims, and the
workflow steps that call `claimed`. They change what runs, so they wait for the owner.
