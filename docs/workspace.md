# Workspace memory

ADR 0025. What the dot learns (`lessons.md`, `memory.md`, `raised.txt` of each repository) lives
in one private git repository per workspace, so the server and your machines share it. `bin/workspace`
is the tool; nothing runs it until you set it up.

**One writer.** `lessons.md` is rewritten whole by `tend-repo`, so only the server publishes it
(`DOTS_WORKSPACE_ROLE=writer`). A reader never edits it: it appends notes to
`inbox/<actor>-<host>-<date>.md`, a file no other machine writes, so nothing conflicts. Only those
three files of `state/tend/<owner>/<repo>/` are copied; a clone, a log, a cursor or a secret is not.

```bash
# once, on the server: a bare repository
ssh dots@server 'git init --bare ~/workspaces/default.git'

# on the server (writer)                       # on a machine you work on (reader)
export DOTS_WORKSPACE_ROLE=writer              export DOTS_WORKSPACE_REMOTE=dots@server:workspaces/default.git
export DOTS_WORKSPACE_REMOTE=~/workspaces/default.git
bin/workspace init                             bin/workspace init
bin/workspace publish                          bin/workspace checkout owner/repo   # pull, install, show the lessons
                                               bin/workspace note owner/repo wi_123 "what I learned"
bin/workspace inbox owner/repo                 bin/workspace release               # push the inbox
bin/workspace archive                          bin/workspace sync                  # pull, install, release
```

`systemd/dots-workspace-sync.{service,timer}` run `sync` every fifteen minutes. Copy
`dots-workspace.env.example` to `~/.config/dots/workspace.env` first. Neither is enabled for you.

`test/workspace.sh` (`make workspace-test`, and in CI) runs a writer and two readers against a local
bare repository: 36 checks, among them that a secret, a clone and a cursor are never copied, that a
reader cannot publish, that two readers release without a conflict, and that a late push rebases.

**Notes are data.** What an agent or a person wrote in a lesson or a note is never an instruction to
another run (ADR 0002).

Not done, because they change what runs: folding the inbox into `lessons.md` and `memory.md` is the job
of `tend-repo` (`bin/workspace inbox` prints what to fold, `archive` moves the folded files aside), and
nothing calls `publish` after a tend pass yet. A `checkout` copies the lessons of a repository, not yet
the worklog of one work item.
