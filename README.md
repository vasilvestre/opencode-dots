# opencode-dots

Runs a dedicated opencode agent and awf on pop-os without installing anything
on the host: one image holds `opencode`, `awf`, `gh`, `awf-gh` and, if you ask
for it, `tower`.

Two containers and one timer:

| Piece | Role |
|---|---|
| `opencode` | `opencode serve` on `127.0.0.1:4096`. The agent itself. |
| `awf` | `awf serve` on `127.0.0.1:2511`. Runs workflows. |
| `awf-gh` | One-shot poll of open pull requests, started by a timer. |

Nothing listens off loopback. Hermes stays the human-facing front: it already
has the bots, and it needs one instruction to call `awf run`. No GitHub watcher
lives inside Hermes: a timer decides, not a model.

## Build and prove it

gh keeps its token in the host keyring, and a container cannot read the
keyring. Every service therefore gets `GH_TOKEN` from `.env`:

```bash
cp .env.example .env
sed -i "s|^GH_TOKEN=.*|GH_TOKEN=$(gh auth token)|" .env
make check
```

`make check` compiles `awf-gh` from `~/projects/oss/awf-gh` into `bin/`, builds
the image, and prints the version of every binary plus `awf serve --help`.
`make check` passing means the stack is real; it does not mean a workflow ran.

```bash
make up     # start opencode and awf
make logs   # follow both
docker compose --profile jobs run --rm awf-gh awf-gh --config /work/awf-gh.yaml --dry-run
```

## Watch list

The repos the watcher polls are private names, and this repository is public.
`awf-gh.yaml` is therefore gitignored and only `awf-gh.example.yaml` is
versioned:

```bash
cp awf-gh.example.yaml awf-gh.yaml
$EDITOR awf-gh.yaml     # add your private repos
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
