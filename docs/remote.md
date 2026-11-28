# Working on a server's opencode from another machine

The server runs its own opencode service (`dots-opencode.service`, ADR 0027), on loopback,
with a password. `bin/remote` reaches it from your machine without a provider key here: the
server's credentials do the work.

```bash
bin/remote dots@server                       # the TUI, on the server's ~/dots
bin/remote dots@server /home/dots/some/dir   # the TUI, on a directory of the server
cd /tmp && bin/remote dots@server run --model mistral/ministral-3b-latest "message"
```

It opens an SSH tunnel to the service's port, reads the password over SSH without printing
or storing it, hands it to opencode in `OPENCODE_SERVER_PASSWORD`, and closes the tunnel when
opencode exits. Nothing is exposed on a network.

## What was checked (2026-11-26, opencode 2.4.1 client, 2.4.2 server)

- The service answers 401 without a password and with a wrong one, 200 with the right one
  (user `opencode`). The password is in `~/.local/state/opencode/service.json`, mode 600.
- `opencode run --server` ran a prompt on the server with the server's own model credentials.
- A client and a server one patch version apart print a warning and work.
- The directory is read on the server. `run` sends the current directory of the client, so
  `run` from a path that does not exist on the server fails with "Location not found"; run it
  from one that exists on both, such as `/tmp`, or use the TUI form with a directory argument.
- `tower` works over SSH as an MCP server: `ssh dots@server tower mcp` answers an `initialize`
  and lists 21 tools. The daemon of a 684-file workspace took 48 MB.

## Not done

- The web UI from a phone. The service serves it; publishing it on the tailnet is one
  `tailscale serve` on the server, and the password is the only gate. Decide that before you do it.
- Claude on the server. `claude` is not logged in there; log it in on the server once.
- Skills, agents and the tower MCP on a server: `deploy/types/local/mcp.sh` and `skills.sh`, see [skills.md](skills.md).

## The hub under a public name (through a proxy you already run)

The hub stays on `127.0.0.1:3901` of the server. A small container next to your reverse proxy
(Dokploy and Traefik, for instance) opens `ssh -L 0.0.0.0:3901:127.0.0.1:3901` to the server with
its own key, and the proxy routes one sub-domain, path `/.well-known/mercure` only, to it with a
certificate. The key on the server is added with
`deploy/types/local/team-access.sh user@host add hub-link KEYFILE --port 3901`: it can open that
one forward and run nothing. The hub refuses an anonymous subscriber (401); a subscriber token
is the next step (the dashboard issuing one after the GitHub sign-in).

The hub binds to loopback itself (`bind 127.0.0.1` in `deploy/hub/Caddyfile`) and accepts any
Host, so the name the proxy forwards does not matter.

### Subscriber token

With `DOTS_HUB_URL` set (the public endpoint) the dashboard answers `GET /api/hub-token` to a
signed-in visitor, or to the owner, with the endpoint and a 15 minute token that names only the
topics `dots/repo/<owner>/<repo>` of the repositories that person may reach (ADR 0046); the hub
key stays in the dashboard, read from the hub's own `~/.config/dots/hub.env`
(`DOTS_HUB_ENV` to change it). A client asks again before the token ends, so a session that is
gone stops following. Without a session the answer is 401, with no hub configured 404.

`DOTS_HUB_AUDIENCE` is the endpoint as the hub sees a request. Behind a proxy that ends TLS the
hub sees `http://` and the forwarded host, so it differs from the public URL:

```
DOTS_HUB_URL=https://hub.example.org/.well-known/mercure
DOTS_HUB_AUDIENCE=http://hub.example.org/.well-known/mercure
```

A hub 1.x subscriber follows `?match=<topic>`, and only an update published `private` is held back
from a subscriber whose token does not name its topic.

### Worker token (a worker nobody watches)

A sign-in session lasts seven idle hours (a day at most, ADR 0053), so an unattended `dots-worker` cannot renew its 15 minute hub token
from it. The owner issues a **worker token** instead, once, where the dashboard runs:

```bash
bin/worker-token issue laptop-ada --login ada --repos org/app,org/other --days 30
bin/worker-token list
bin/worker-token revoke ID
```

The secret (`dwt_…`) is printed once and kept nowhere: the dashboard stores only its SHA-256, in
`STATE_DIR/worker-tokens.json` (mode 0600). It binds a label, a login and the repositories the
worker may follow (none listed: every repository the dot knows), lives 30 days by default and 90
at most, and `revoke` ends it at once: the next exchange answers 401, and the hub drops the worker
within the 15 minutes of the token it holds.

The worker trades it for a hub token, again before each one ends:

```
GET /worker-token    Authorization: Bearer dwt_…
```

That exchange is served on the worker listener (`-worker-addr` or `DOTS_WORKER_ADDR`, a loopback
IP and a port, for example `127.0.0.1:8096`), which serves this one path and 404s everything else,
so the token opens nothing of the dashboard. The hub's own Caddy relays it: `GET /worker-token` on the
hub's public name reaches that listener (`DOTS_WORKER_UPSTREAM` changes where), so one tunnel and one
route on the proxy carry both the hub (`/.well-known/mercure`) and the exchange (`/worker-token`).
Anything else on that name answers 404.
A wrong token costs half a second, and nothing of a token is logged.

## The queue and dots-worker

`dots-worker` (`cmd/worker`, `make go-build` writes `bin/worker`) runs on a machine that has the
local `awf serve` and nothing else to set up: one outbound HTTPS connection to the server, no
tunnel, no open port.

**Server.** The watcher posts its runs to the dashboard instead of an awf: set its executor to
`http://127.0.0.1:8099/queue` (`settings.executors` in `repos.yaml`, then `bin/repos generate`).
`POST /queue/api/workflows/{scope}/{workflow}/run` keeps the run in `STATE_DIR/jobs.json` (mode 0600)
and answers 202 with a job id, the way awf does; `DELETE /queue/api/executions/{id}` cancels. Both are
owner only, so only the loopback watcher reaches them. `GET /api/queue` (owner) lists every job.

**Worker.** It reads `DOTS_WORKER_URL` (the server's worker entry, `https://hub.example/worker`)
and the token in `~/.config/dots/worker-token` (or `DOTS_WORKER_TOKEN`), and does:

1. follow the hub for the token's topics (a message only wakes it) and read the queue every minute,
   so a missed message delays a job and never loses it;
2. claim a job (`POST /api/jobs/{id}/claim`): the first worker wins, the others get 409;
3. run it on the local awf, renew the claim every 30 s (`heartbeat`), which also tells it when the
   watcher cancelled the run;
4. report `done` or `failed` (`finish`) with a note.

A claim is a lease of two minutes: a worker that vanishes gives the job back to the others. A job nobody
takes within 24 hours expires, so a worker that comes back after days runs no stale work. A worker only
sees the jobs of the repositories its token names. Each event is also published on the repository's
private topic (`job`, `taken`, `finished`, `cancelled`) for whoever follows the hub.

```bash
DOTS_WORKER_URL=https://hub.example/worker bin/worker          # the daemon
DOTS_WORKER_URL=https://hub.example/worker bin/worker -once    # run what is pending, then stop
```

The hub's Caddy relays `/worker/*` to the worker listener (prefix removed), next to the older
`/worker-token`.
