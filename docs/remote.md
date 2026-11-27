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

A sign-in session lasts an hour, so an unattended `dots-worker` cannot renew its 15 minute hub token
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
