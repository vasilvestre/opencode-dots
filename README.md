# opencode-dots

Runs a dedicated opencode agent and awf on pop-os without installing anything
on the host: one image holds `opencode`, `awf`, `gh`, `awf-gh` and, if you ask
for it, `tower`.

Two containers and one timer:

| Piece | Role |
|---|---|
| `opencode` | `opencode serve` on `127.0.0.1:4096`. The agent itself. |
| `awf` | `awf serve` on `127.0.0.1:8080`. Runs workflows. |
| `awf-gh` | One-shot poll of open pull requests, started by a timer. |

Nothing listens off loopback. Hermes stays the human-facing front: it already
has the bots, it only needs one instruction to call `awf run`.
No GitHub watcher lives inside Hermes: the timer decides, not the model.

## Build and prove it

```bash
make check
```

`make check` compiles `awf-gh` from `~/projects/oss/awf-gh` into `bin/`, builds
the image, prints every version, and runs `awf serve --help`. Read that output
before `make up`: the `awf serve` flags in `compose.yaml` are the one thing not
verified yet.

```bash
make up     # start opencode and awf
make logs   # follow both
```

## The agent is in this repo

`agent/` is mounted as the container's opencode config root, so the bot's
instructions, agents and skills are versioned here and never mixed with your
personal `~/.config/opencode`.

- `agent/AGENTS.md`: the standing rules.
- `agent/agents/assistant.md`: the agent, its model and its permissions.

## Systemd

```bash
mkdir -p ~/.config/systemd/user
cp systemd/* ~/.config/systemd/user/
systemctl --user daemon-reload
systemctl --user enable --now dots.service
systemctl --user enable --now dots-awf-gh.timer
```

`dots.service` runs `docker compose up -d` and stays active. The timer runs one
`awf-gh` pass every two minutes. The dedup state lives in a named volume, so a
`compose down` does not lose it.

## Security

The agent has a shell inside a container that mounts `~/projects` and your gh
auth. Treat the opencode port as a root shell on your files: keep it on
loopback, and do not expose it through a tunnel without authentication.

The PR workflow reads untrusted text. The agent step writes a review; a shell
step posts it with `gh pr comment`. The agent never runs the PR's tests.

## Tower

Tower indexes a workspace in a daemon and exposes it to agents over MCP. It
overlaps what opencode already does (`read`, `glob`, `grep`, `edit`), so the
gain shows up only on large or multi-repo workspaces. It is `v0.1.0`.

Test it before baking it in:

```bash
make check                                 # image without tower
docker run --rm -it -v "$HOME/projects:/workspace" opencode-dots:latest bash
# inside: curl the tower tarball, run it on one small repo, see what it buys
```

Then build with `--build-arg WITH_TOWER=1` and declare it in
`agent/opencode.jsonc` as an MCP server. The Dockerfile already knows where the
tarball is.

## Next:

```bash
cd ~/projects/perso/opencode-dots
make check
```
