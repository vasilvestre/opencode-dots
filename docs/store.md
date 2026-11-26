# The control-plane database

ADR 0024. One SQLite file, `$STATE_DIR/dots.db` (mode 600, gitignored with `state/`), holds
the projects, the work items and the references that tie a work item to a tracker. This is
step 1 of its migration: `repos.yaml` is still the source, and the database is loaded from it.

```bash
bin/repos db import        # load repos.yaml (resolved: defaults and profiles applied)
bin/repos db import -prune # also drop projects the file omits, unless work items name them
bin/repos db check         # do the database and the file generate the same files? exit 1 if not
bin/repos db list          # the projects and their feature keys
bin/repos db export        # a repos.yaml from the database, on stdout
```

`-workspace NAME` picks the workspace (default `default`) and `-db FILE` the file.

What is stored: `workspace` (its watcher settings, executors included), `actor`, `project`
(repository, order, resolved features as JSON), `work_item` and `work_item_ref`. A reference is
`tracker:key` (`github:owner/repo#12`, `branch:owner/repo:name`, `jira:ABC-12`) and is unique
across the table: one reference names one work item (ADR 0023). Every row that records a change
names an actor.

Not done yet, in the order of the ADR: generating from the database instead of the file (the
code exists, `BuildFromStore`, and `db check` is the proof it agrees), the dashboard editing
through the database, and an adapter that creates work items from GitHub events.

The export writes each project's resolved features, not the original defaults and profiles, so
it is not a drop-in for a hand-written file with `use:` profiles.
