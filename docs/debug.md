# Reading a run with `dots`

`bin/dots` reads what a run left behind, for a person or an agent who debugs it.
It only reads: `state/logs` (the dashboard's recording) and awf's transcripts.
Build it with `make go-build`. `--state DIR` overrides `STATE_DIR`.

```bash
bin/dots runs --failed --limit 5          # newest first; --workflow, --json
bin/dots show last                        # steps, first failure, its last events
bin/dots logs 00731462 --step review --tail 20
bin/dots logs last --kind step.failed --json   # one JSON event per line
```

- A run is named by its id, a unique prefix, or `last`.
- `show` exits 1 when the run failed, 0 otherwise, 2 on a usage or read error.
- The first failure is the first step to fail in event order. The step that wraps
  it (`rework` around `review`) fails after it and only echoes its error.
- A step that loops appears once per pass. Durations need awf's transcript,
  which carries timestamps; the dashboard recording has none.
- `--json` prints stable keys (`id`, `workflow`, `status`, `failed_step`,
  `steps[]`) for scripts and agents.

Source: `cmd/dots`, `internal/runlog`. The dashboard keeps its own readers.

The image carries `dots` in `/opt/agent/bin`, and `agent/skills/debug-dots-run` tells the
agent when and how to use it. `agent/` is mounted as the opencode config, so the skill is
present by default. Only the awf service mounts `state/`; elsewhere pass `--state`.
