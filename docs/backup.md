# Backing up a dot server

`bin/backup-server user@host [DEST_DIR]` pulls a dated archive from your machine. The server
holds nothing else that cannot come back.

| In the archive | Why |
| --- | --- |
| `state/tend/` | lessons, raised findings, cursors, worklogs: what the dot learned |
| `repos.yaml` | the list of repositories and what the dot does on each |
| `workspaces/` | the bare git repository of the workspace memory, with its history (ADR 0025): the only place that keeps a lesson's earlier versions |

Left out on purpose: the clones (`state/*/work`) and logs, which come back from GitHub, and
`.env` and the opencode credentials. They are secrets, and a backup of them is one more place
to leak from. After a restore, log in again (`gh auth login`, `opencode auth login`).

The script reads the archive back before it keeps it, so a cut connection never replaces a
good backup, and it keeps the newest 14 (`DOTS_BACKUP_KEEP`).

To run it daily from the workstation, put the server in `~/.config/dots/executor.env`
(`DOTS_SERVER=user@host`): the `backup-server` task of `bin/tick` ([units.md](units.md)) then runs it once a day,
and does nothing when `DOTS_SERVER` is not set. `bin/tick run backup-server` runs it now.

To restore, copy the archive to the server and extract it in the home directory (the paths in the
archive start there): `tar -xzf dots-<date>.tgz -C ~`.

Not covered: the awf run history (`~/.local/share/awf/history.db`, a live SQLite file that a
plain copy can corrupt) and the opencode sessions. A snapshot of the machine at the provider
also covers them, and costs money.
