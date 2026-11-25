# 0016. awf-gh can run natively

- Status: accepted
- Date: 2026-11-25
- Supersedes: none
- Superseded by: none

## Context

For an install where `opencode`, `awf` and `gh` already run on the host, the
image carried one thing: `awf-gh`, started as a container every two minutes.
`awf-gh` is a single Go binary that calls `gh`, talks HTTP to awf and appends to
a file. Keeping it in an image meant keeping the image, and a rebuild timer to
stop it drifting. That timer ran a build that reached `GH_TOKEN` and downloaded
unpinned binaries: a supply-chain exposure for a convenience nobody needed.

## Decision

We let `awf-gh` run natively. `bin/configure` gets a fourth piece, `awf-gh`
(`--awf-gh=local|docker`, `DOT_AWFGH` in `.env`), defaulting to `docker` so
nothing changes for existing installs. `systemd/dots-awf-gh-local.service`
runs `bin/awf-gh` with a host-path config; a drop-in on the existing timer
points it there. `awf-gh=local` forces `gh=local`. When every piece is local the
image and any rebuild timer are unnecessary, and the rebuild timer is not added.

## Consequences

- No image, no container and no downloaded binary in the polling path.
- `awf-gh` must be built with `make build`; configure refuses `local` without it.
- A second config shape: `state_path` and `awf_url` are host values, shown in
  `awf-gh.local.example.yaml`.
- The native process needs `gh` on the systemd user `PATH`.
- Docker mode is unchanged and still supported.

## Alternatives

- **Keep the rebuild timer, pin the downloads.** Still a token-bearing build on
  a schedule for a binary we can build once by hand.
- **Drop docker mode for awf-gh.** Breaks installs that use it today.
