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
3. As the user: `deploy/types/local/user.sh`. On a machine that is on the tailnet it also writes `DOTS_ALLOW_HOST` (the tailnet name) for the dashboard, or every write from the page answers "refused origin". It stops before `bin/configure` if the
   user is not logged in to GitHub; run `gh auth login --with-token` with a token limited to
   the repositories the machine may touch, then run it again.
4. From your workstation: `deploy/types/local/push.sh dots@server --tower --repos-yaml repos.yaml`.
   `repos.yaml` is private: it names your repositories.
5. Enable the timers you want, for example `systemctl --user enable --now dots-tend.timer`,
   or `DOTS_ENABLE_TEND=1` on a second run of `user.sh`.

`user.sh` can be run again to update the repository and the units. The units in
`systemd/` name the workstation's path: the script writes copies for this machine and
leaves the originals alone.

## Size

Take at least **2 GB**. A 1 GB machine (Scaleway's STARDUST1-S) froze for a minute under a tend pass and had to be
powered off: at rest the opencode service holds 448 MB of the 962, and every project directory a run opens leaves an
instance in it until it restarts (147 MB with none, 478 MB after three). `user.sh` restarts the service before each
tend pass and sets a hard `MemoryMax` (600 MB on the opencode service under 1.5 GB, 1200 MB under 4 GB); it never sets a soft `MemoryHigh`, which makes the kernel
reclaim in a loop. If the agent steps run on another machine (ADR 0031), 1 GB is enough for the rest.

## Moving to a bigger server

1. Create it with `providers/scaleway` (`type = "DEV1-S"` in your ignored `terraform.tfvars`), or by hand.
2. `system.sh`, then `user.sh` on it, then `push.sh` from the workstation with `--tower --repos-yaml`.
3. Log in again on the new machine (`gh auth login`, `opencode auth login`): `bin/backup-server` leaves secrets out.
4. Restore `state/tend` from the latest archive: `tar -xzf dots-<date>.tgz -C ~/dots`. Never copy `state/*/work`.
5. Run one `bin/tend --dry-run`, enable `dots-tend.timer`, and only then retire the old server.

## opencode memory over time

`user.sh` writes `~/.config/opencode/opencode.jsonc` with `experimental.location_idle_timeout` at 5 minutes
(`DOTS_OPENCODE_IDLE`), unless a config already exists. Measured with three project directories opened: 428 MB
stays at the default of 60 minutes, 242 MB with 1 minute, and a server with no project is 147 MB. For a hard reset
of what a run loaded, `DOTS_OPENCODE_STANDALONE=1` runs each step on a private server that stops with it: it
frees everything, costs about 8 s a start and peaked at 744 MB on a large repository, so it is not the default.
