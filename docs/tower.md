# Tower (agent memory MCP)



Tower indexes a workspace in a daemon and exposes it to agents over MCP, plus a
durable agent-memory store. The Linux release tarballs are still empty (PR
awf-project/tower#99), so build it from source:

```bash
git clone https://github.com/awf-project/tower /tmp/tower-src
cd /tmp/tower-src
make install                 # tower -> ~/.local/bin
make install-extensions GLOBAL=1
```

The `agent_memory` extension gets storage access only when its binary sits next
to `tower`, and `make install` does not copy it yet (PR awf-project/tower#100).
Until that lands, link it:

```bash
ln -sfn ~/.local/share/tower/extensions/agent_memory/agent_memory_extension \
  ~/.local/bin/agent_memory_extension
```

Then declare `tower mcp` in `~/.config/opencode/opencode.jsonc` under
`mcp.servers`. With `DOT_OPENCODE=local` that is enough: the opencode server
runs on the host, so it finds `tower` on `PATH`. `tower init` in a repository
scaffolds `.towerignore` and `.tower/config.toml`; without `tower init` the
daemon warns and indexes every non-hidden file.

The image build keeps `WITH_TOWER=0` by default. Since v0.1.2 the release
tarball carries the binary and its extensions, so `WITH_TOWER=1` yields a
working tower. The default stays off because tower is a bonus, never a
requirement: the workflows write their own memory of a thread to
`state/replies/pr<N>/worklog.md` and `state/fix/<repo>/issue-<N>/worklog.md`,
and the agent reads and rewrites that file on every run. When tower memory is
reachable, the same agent also stores one `session_summary` record per run with
the pull request or issue number as provenance.
