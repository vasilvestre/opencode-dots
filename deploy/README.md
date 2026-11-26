# deploy/

A server as code (ADR 0028), in two layers that do not depend on each other.

| Path | Needs | Does |
| --- | --- | --- |
| `providers/<name>/` | the provider's credentials | OpenTofu: creates the machine. Optional. Only `scaleway` exists. |
| `types/local/system.sh` | root, once | packages, the `dots` user and its key, linger, zram, optional SSH hardening and Tailscale |
| `types/local/user.sh` | no root | awf (release binary, checksum verified), opencode v2, the repository in `~/dots`, the user units |
| `types/local/push.sh` | a workstation | builds and sends `bin/repos`, `bin/dashboard`, and with `--tower` tower and its extension |
| `test/run.sh` | docker | runs both steps twice in a Debian 13 container (`make deploy-test`) |
| `versions.env` | | the versions the scripts install |

Nothing here compiles on the server, needs Docker there, or needs `/dev/kvm`. No secret is
written to a tracked file: tokens come from the environment or are typed on the server.

## A new server

1. Create the machine: `providers/scaleway` (see its README), or any Debian 13 box.
2. As root on it:

   ```bash
   sudo DOTS_SSH_PUBKEY="$(cat ~/.ssh/id_ed25519.pub)" DOTS_SUDO_NOPASSWD=1 deploy/types/local/system.sh
   ```

   Add `TS_AUTHKEY=... TS_HOSTNAME=... TS_TAGS=tag:dots` to join the tailnet, and
   `DOTS_HARDEN_SSH=1` once you can log in as the user (test a second session first).
3. As the user: `deploy/types/local/user.sh`. It stops before `bin/configure` if the
   user is not logged in to GitHub; run `gh auth login --with-token` with a token limited to
   the repositories the machine may touch, then run it again.
4. From your workstation: `deploy/types/local/push.sh dots@server --tower --repos-yaml repos.yaml`.
   `repos.yaml` is private: it names your repositories.
5. Enable the timers you want, for example `systemctl --user enable --now dots-tend.timer`,
   or `DOTS_ENABLE_TEND=1` on a second run of `user.sh`.

`user.sh` can be run again to update the repository and the units. The units in
`systemd/` name the workstation's path: the script writes copies for this machine and
leaves the originals alone.
