# Executors: watch on one machine, run on another

ADR 0031. A server can watch GitHub around the clock while the tasks run on
your own machine, with its `opencode`, its Claude and its GitHub identity. The
watcher (`awf-gh`) stays on the server; each repository names the `awf serve`
that runs its tasks.

```
GitHub <- awf-gh (server) --POST--> 127.0.0.1:2512 (server) ==ssh tunnel==> awf serve (your machine)
```

## Set it up

1. On the server, name the executor in `repos.yaml` and give a repository its
   rule and its executor. The address is the tunnel's port on the server's
   loopback.

   ```yaml
   settings:
     state_path: /home/<user>/dots/state/awf-gh/seen.jsonl
     executors:
       workstation: http://127.0.0.1:2512
   repos:
     your-org/your-repo:
       review: true
       executor: workstation
   ```

   `bin/repos generate` writes `awf-gh.workstation.yaml` (and no `awf-gh.yaml` if
   no repository runs on the server itself). `tend` always runs on the server.

2. On your machine, copy `systemd/dots-executor.env.example` to
   `~/.config/dots/executor.env`, set `DOTS_SERVER`, and install the tunnel unit:

   ```bash
   install -m 644 systemd/dots-executor-tunnel.service ~/.config/systemd/user/
   systemctl --user daemon-reload
   systemctl --user start dots-executor-tunnel.service
   ```

3. On the server, check the tunnel and the watcher, then enable its timer.

   ```bash
   curl -s http://127.0.0.1:2512/api/workflows      # your machine's workflows
   bin/awf-gh --config awf-gh.workstation.yaml --dry-run   # what it would dispatch
   bin/awf-gh --config awf-gh.workstation.yaml --seed      # first time: mark the open PRs as seen
   systemctl --user enable --now dots-awf-gh-executor@workstation.timer
   ```

## Behaviour

- The tunnel opens a port on the server's loopback only (sshd keeps
  `GatewayPorts` off). Nothing listens on your machine, and the `awf` API, which
  has no authentication, is reachable only from the server.
- When your machine is off, the dispatch fails and `awf-gh` does not record the
  event, so every pass retries it. After a long absence the backlog arrives at
  `max_per_run` events per pass.
- The server's `GH_TOKEN` only has to read pull requests and issues. The steps
  that comment, push and open pull requests run on your machine with its token.
- `awf-gh` refuses to start without its seen file, so the first pass never
  dispatches the backlog by accident. Run `--seed` to mark the open pull requests
  as seen, or `touch` the file (`state/awf-gh/seen-<executor>.jsonl`) to let the
  open pull requests be dispatched on that first pass.
