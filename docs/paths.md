# Where a repository lives on this machine

A workflow that needs a repository's files reuses a clone already on the machine, and falls back to a private
clone under `WORK_ROOT` (default `STATE_DIR/work`). `bin/where OWNER/REPO` is the one place that answers
"is it here, and where". `review-pr`, `fix-issue`, `fix-ci`, `reply-pr` and `tend-repo` call it.

```bash
bin/where example-org/webapp            # the path of a clone, or nothing and exit 1
bin/where --suggest example-org/webapp  # where it should live, from the machine map
```

## The machine map: `~/.config/dots/paths`

One line per rule, `PATTERN=PATH`. `~` is your home directory, `#` starts a comment.

```
example-org/*=~/example-org          # every example-org repository is ~/example-org/<name>
acme/tool=~/work/acme-tool           # one repository, anywhere
*/*=~/projects                       # any other owner: ~/projects/<owner>/<name>
```

Looked up in this order, the first real clone wins:

1. `repos.map`, the explicit `checkout:` of `repos.yaml` (the owner's own configuration, trusted as written);
2. the lines of the machine map, in file order.

A candidate counts only if it is a Git clone whose `origin` is that repository (case does not matter, https or
ssh), so a folder that merely has the same name is never taken. With nothing found the workflow clones under
`WORK_ROOT`, as before: an unmapped project is never an error.

`DOTS_PATHS` changes the file. The map lives on **each machine**, not in `repos.yaml`: a developer who only works
for a team's server has no `repos.yaml` of their own, and keeps their projects where they like.

## What a reused checkout is

It is the developer's own working tree. A workflow only fetches into references private to its run, or works in a
`git worktree` of its own; it never moves, pulls or resets what is checked out. Keep it so when a step is added.

## Not built yet

- A card in the local dashboard listing the repositories a worker serves, found here or not, with a clone button.
- A `strict` repository may keep no local checkout (ADR 0032, 0050); the resolver does not know about trust yet, so
  for such a repository the checkout simply must not be mapped.

Tests: `bin/test-where`, and the e2e cases that reuse a checkout (`bin/test-e2e`).

## The team's suggestion

A team server can say where its projects usually live, without imposing it. On the server, in `repos.yaml`:

```yaml
settings:
  checkout_hints: {example-org: "~/example-org"}   # a folder under ~/ or an absolute path, no ..
```

`dots-worker` asks `GET /worker/api/hints` (the same token as the rest, and only the owners of the repositories
that token covers) at start and every hour, and writes `~/.config/dots/hints`, one `owner/*=folder` line each. Then:

- `bin/where` looks in that file **after** your own `paths`, so a project cloned where the team suggests is found,
  and your own map always wins;
- `bin/where --suggest` falls back on it when you have no root of your own for that owner;
- when a job arrives for a project that is not on the machine, the worker's log says so and gives the place and the
  `git clone` command. The run goes on with a private clone: nothing waits.

The worker never clones by itself and never creates a folder.
