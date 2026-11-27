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

- A recommendation from the team ("our projects go in `~/example-org`") shown on the machine of someone who has not
  got the project.
- A card in the local dashboard listing the repositories a worker serves, found here or not, with a clone button.
- Both depend on how `trust` places code (ADR 0032, under discussion): a `strict` repository may be local-only.

Tests: `bin/test-where`, and the e2e cases that reuse a checkout (`bin/test-e2e`).
