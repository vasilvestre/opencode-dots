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
