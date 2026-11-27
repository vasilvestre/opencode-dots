# Running in Docker, or natively

> Docker mode is the original way to run the stack and is not the supported one: the supported install is `bin/dots-units` ([units.md](units.md)), which runs `awf`, the dashboard and the watchers natively. This page keeps what is still true of the image and of the `local` and `docker` choices.

`make configure` asks a few questions and writes `.env` and
`compose.override.yaml`.

| Piece | `local` | `docker` |
|---|---|---|
| `opencode` | attaches to the `opencode serve` already running on your host | starts `opencode serve` in a container |
| `awf` | uses an `awf serve` that you start on the host | starts `awf serve` in a container |
| `gh` | mounts the host `gh` binary, no version drift | uses the `gh` baked into the image |
| `awf-gh` | runs `bin/awf-gh` on the host (needs `make build`) | runs the one-shot container (default) |
| `claude` (optional, `off` by default) | mounts the host `claude` CLI and `~/.claude`, no version drift | uses the `claude` CLI baked into the image |

`claude` is a CLI `awf` shells out to per step (`provider: claude` in a
workflow), not a server: there is no `claude serve` to attach to, so `local`
just means "use the host binary and credentials" the same way `gh` does.
Opting in does not replace opencode; workflows that should use Claude Code
instead of opencode set `provider: claude` themselves.

Docker mode is experimental and not encouraged. `local` is the supported path:
the image builds and the binaries answer, but nothing runs it on a schedule,
tower memory is not available in a container, and the provider credentials path
is untested. Start with `local` and stay there unless you are changing the
image.

## Run everything natively

With `opencode`, `awf`, `gh` and `awf-gh` all `local`, the image is never used:
skip `make image`, `make up` and any rebuild timer. `awf-gh=local`
forces `gh=local`.

```bash
make build                                   # bin/awf-gh from $AWF_GH_SRC
bin/configure --awf=local --awf-gh=local     # regenerates awf-gh.yaml from repos.yaml with host paths
systemctl --user edit dots-awf-gh.timer      # [Timer] Unit=dots-awf-gh-local.service
```

`systemd/dots-awf-gh-local.service` replaces `dots-awf-gh.service`; the timer
stays. It names `~/projects/perso/opencode-dots`: for a checkout elsewhere,
replace that path in the copied unit with `sed -i "s|%h/projects/perso/opencode-dots|$PWD|g"`. `make awf-gh` runs the native binary when `.env` has `DOT_AWFGH=local`.

What `awf-gh` needs at runtime, read from its source: `gh` on `PATH` (`gh pr list`,
`gh api`, `gh issue list`) with `GH_TOKEN` or a logged-in keyring; HTTP to
`awf_url`; write access to `state_path` and its `.exec` sidecar (the directory is
created). No `git`, no `jq`. Flags: `-config`, `-dry-run`, `-seed`, `-forget`.

The gh token always comes from `.env`: a container cannot read your keyring.

`opencode=local` reuses your running server, so the awf container joins the
host network and mounts `state/` at the same absolute path. The opencode server
runs tools on the host and sees host paths only. Point it at a container path
and the agent will not find `$STATE_DIR`.

```bash
make configure
make check
```

`make check` compiles `awf-gh` from `~/projects/oss/awf-gh` into `bin/`, builds
the image, and prints the version of every binary plus `awf serve --help`.
`make check` passing means the stack is real; it does not mean a workflow ran.

```bash
make up     # start what your modes selected
make logs
docker compose --profile jobs run --rm awf-gh awf-gh --config /work/awf-gh.yaml --dry-run
```
