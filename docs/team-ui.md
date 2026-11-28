# The dashboard for a team over SSH

A team with no tailnet can reach the dashboard through an SSH local port forward. This page says how,
and why the dashboard needs a second listener for it. Sign-in itself is described in
[dashboard-auth.md](dashboard-auth.md).

## Why a second listener

The owner is a request that reaches the dashboard directly on loopback, with a loopback `Host` and no
tailnet login header. An SSH tunnel to the owner port (8099) ends on the server's loopback too. A developer
who forwarded 8099 would therefore be the owner: they could edit the configuration and release held work.

So the owner port is never forwarded. The **team listener** is a second port, on loopback as well, where
**every request is a visitor**, whatever its source address, `Host` or headers. The dashboard knows it
from the listener the request arrived on, not from anything the client sends. It serves the same routes, the
same GitHub device-flow sign-in and the same repository filtering as for a visitor through the tailnet:
a developer sees only the repositories where GitHub gives them push (or the configured permission).
Owner-only endpoints (`/api/save`, the config pages, release, ask, the `/awf/` proxy, health, workers,
`/api/links` writes, Activity) answer 401 before sign-in and 403 after, as for any visitor.

On this listener the `Host` must be a loopback one, with any port (the developer picks the local port of
their forward); anything else is 403. A POST that carries an `Origin` must have the same host as the
request's `Host`. Cookies are as ever: `HttpOnly`, `SameSite=Strict`, `Secure` only over https.

## Set it up on the server

1. Sign-in must already be on: `DOTS_GITHUB_CLIENT_ID` is set (see dashboard-auth.md). The team listener
   refuses to start without it.
2. A systemd drop-in for the dashboard unit:

   ```
   # ~/.config/systemd/user/dots-dashboard.service.d/team.conf
   [Service]
   Environment=DOTS_TEAM_ADDR=127.0.0.1:8098
   ```

   then `systemctl --user daemon-reload && systemctl --user restart dots-dashboard.service`.
   The flag is `-team-addr`. The dashboard stops at start, with a message, when the address is not a
   loopback IP address (`127.0.0.0/8` or `::1`; a host name is refused), when sign-in is off, or when it
   equals the owner address. With the variable unset nothing changes.

## The admin adds and removes a developer

From the admin's machine (it needs ssh access to the server user, and no root):

```bash
deploy/types/local/team-access.sh dots@HOST add alice ~/alice.pub            # port 8098
deploy/types/local/team-access.sh dots@HOST add alice ~/alice.pub --port 8098 --dry-run   # print the line only
deploy/types/local/team-access.sh dots@HOST list
deploy/types/local/team-access.sh dots@HOST remove alice
```

`add` writes one line in the server user's `~/.ssh/authorized_keys`:

```
restrict,port-forwarding,permitopen="127.0.0.1:8098",command="/bin/false" ssh-ed25519 AAAA... dots-team:alice
```

The name is `[a-z0-9][a-z0-9-]{0,30}`. The file must hold one public key with no options (a private key, a
file with several keys and a key that already carries options are refused). Adding a name again replaces
its line. Only lines tagged `dots-team:NAME` are touched; the file is written through a temporary file in
the same directory, a timestamped copy is kept beside it (`authorized_keys.bak-<time>`), the file stays
`600` and `~/.ssh` `700`. `list` prints each name with its port and key fingerprint, never a key body.

## A developer connects

```bash
ssh -N -L 8098:127.0.0.1:8098 dots@HOST
```

Then open `http://127.0.0.1:8098`, press sign in, enter the code on github.com, and the page shows the
repositories where they can push. Any free local port works (`-L 9000:127.0.0.1:8098`, then
`http://127.0.0.1:9000`).

## Limits and notes

- A restricted key can only open that tunnel: no shell (`command="/bin/false"`, `restrict`), no agent or X
  forwarding, no other port than the one in `permitopen`. Revoke by `remove`; an open tunnel ends when
  its connection drops, so also end sessions on the server if needed (`sessions.json`, see dashboard-auth.md).
- A session lasts seven idle hours and a day at most (ADR 0053). A right removed on GitHub disappears at the latest then.
- The key opens the tunnel, not the dashboard: the developer still signs in with their own GitHub account.
- NetBird later would only change how a developer reaches the server. The team listener stays on loopback.

**Security note.** Never forward the owner port (8099) to developers, and never bind the team listener to a
non-loopback address. An SSH tunnel to 8099 makes whoever holds it the owner. The `permitopen` of every
key written by `team-access.sh` is the team port only.
