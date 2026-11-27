# Skills, agents and the memory MCP on a server

Two scripts under `deploy/types/local/` close two gaps of a server that `user.sh` and
`push.sh` set up. Run both on the workstation.

## mcp.sh: the memory MCP

```bash
deploy/types/local/mcp.sh dots@server
```

It merges one entry into the server's `~/.config/opencode/opencode.jsonc`, and no other key:

```json
"mcp": { "tower": { "type": "local", "command": ["tower", "mcp"], "enabled": true } }
```

It writes a temporary file beside the config, checks that it parses, keeps the old one as
`opencode.jsonc.bak-<timestamp>`, renames the new one in, restarts `opencode.service` and prints
`systemctl --user is-active`. A second run changes nothing, says so and restarts nothing.

- It refuses when `~/.local/bin/tower` is missing: run `push.sh --tower` first.
- It refuses a config that is not plain JSON (one with `//` comments, say) and leaves it alone.
  Add the entry by hand in that case.
- `user.sh` writes the same entry in the config it creates, when tower is already installed. Both
  use one function, `merge_tower_mcp` in `deploy/lib.sh`.
- Only tower goes to a server. The other MCP servers of a workstation hold personal keys.

## skills.sh: the team's skills and agents

```bash
deploy/types/local/skills.sh dots@server --skills-repo ~/path/to/clone [--commit REF] [--dry-run]
```

The shared skills, agents and rules live in a private repository. The script:

1. checks that the clone is a git work tree with a `.skillshare` directory, resolves `REF`
   (default `HEAD`) to a full commit, and refuses uncommitted changes under `.skillshare`, so the
   snapshot equals the commit;
2. copies the local `skillshare` (x86_64, static, it prints the version) to
   `~/.local/bin/skillshare`, under a temporary name and then renamed, like `push.sh`;
3. sends `git archive REF .skillshare` into `~/skills.new`, without any `mcp.yaml` or `config.yaml`,
   writes `.snapshot` (the commit and the date), and swaps it into `~/skills`; the previous one stays
   as `~/skills.prev`;
4. writes `~/skills/.skillshare/config.yaml` with two targets, opencode skills at
   `$HOME/.config/opencode/skills` and opencode agents at `$HOME/.config/opencode/agents`, both in
   `merge` mode, as absolute paths (a target does not accept `~`);
5. runs `skillshare sync -p` and `skillshare sync agents -p` in `~/skills`, then `skillshare status -p`.

It is safe to run twice. `--dry-run` runs the local checks, prints what it would send and
touches nothing on the server.

### Why a snapshot and not a clone

A clone needs a credential for the private repository on the server. A server that runs agents
should hold as few as possible. The workstation already has access and sends the files, pinned to
a commit, so the server cannot drift and `.snapshot` says what it runs.

### Update, roll back

```bash
deploy/types/local/skills.sh dots@server --skills-repo ~/path/to/clone --commit NEW
```

To roll back, on the server: `rm -rf ~/skills && mv ~/skills.prev ~/skills`, then
`cd ~/skills && skillshare sync -p && skillshare sync agents -p`. Or send the old commit again.
The links in the opencode directories point at `~/skills/.skillshare/...`, so they follow the swap.

### What is deliberately not synced

- MCP definitions: `mcp.yaml` is never sent, the server config has no `mcp` section, and
  `sync mcp` is never run. Personal servers hold personal keys. Tower alone comes from `mcp.sh`.
- Extras (rules) and any registration of a client project: not done by this script.
- No `~/.claude` target and no universal target.

## Skills specific to a client project

A project-specific skill lives in the shared repository under a group prefix (`<prefix>__*` for
skills, `<prefix>-*` for agents). The skillshare project mode registers it for one repository,
as the team repository's README describes. On the server, for each client repository:

```bash
skillshare project add ~/skills --target opencode \
  --skills-include '<prefix>__*' --agents-include '<prefix>-*' --machine-local
skillshare project sync
```

This is run by hand, once per client repository. It is not automated here.
