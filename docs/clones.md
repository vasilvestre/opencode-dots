# Where the clones live

See also [paths.md](paths.md), which says how a workflow finds a checkout that is already on the machine.


The review and fix workflows never touch your own checkouts. They fetch the
pull request branch into a clone and add a detached worktree for the run:

- `WORK_ROOT` (default `$STATE_DIR/work`) holds the clones, one per repository,
  reused across pull requests. Set it in `.env` unless you also mount it: a
  local opencode server only reads host paths.
- Each run works in its own worktree under `$STATE_DIR/replies/pr<N>/work`, so
  two pull requests on the same repository never share a working tree.
- A repository's `checkout:` in `repos.yaml` (generated into `repos.map`) makes
  a run reuse an existing checkout instead of cloning:

```yaml
repos:
  owner/repo:
    checkout: /absolute/path
```

The workflow only fetches objects into that checkout and adds a worktree. Your
branch, your index and your working tree stay as they are: no `git pull`, no
checkout, no stash.

Replaying one event. The watcher records a key per dispatched event, so it
never answers the same comment twice. To replay one on purpose, drop its key
and let the next run see it again:

```bash
docker compose --profile jobs run --rm awf-gh \
  awf-gh --config /work/awf-gh.yaml --forget 7086166143
systemctl --user start dots-awf-gh.service
```

`--forget` matches any part of a key, so a comment id or `owner/repo#42` both
work. Forget a comment id, not a whole pull request: a repository met for the
first time only fires `pr-opened`.
