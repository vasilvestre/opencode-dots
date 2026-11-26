# 0024. A database owned by dots is the source of truth for the control plane

- Status: accepted
- Date: 2026-11-26
- Supersedes: 0022
- Superseded by: none

## Context

[0022](0022-repos-yaml-is-the-source-of-truth.md) made `repos.yaml` the one
file to edit, and generated `awf-gh.yaml`, `tend.list`, `repos.map` and
`sync.map` from it. That fits one person and one machine. We now plan an
always-on VPS that handles repositories while the workstation is off, and a
work item that does not depend on the tracker
([0023](0023-tracker-independent-work-item.md)).

A file does not fit that:

- Work items are created by events, many times a day, from several processes
  (the poller, the dashboard, a workflow step). A file edited by hand and
  rewritten by tools has no safe concurrent writer.
- Work items carry references, actors, and soon claims with an expiry and a
  heartbeat. These are rows with relations and queries ("which item owns this
  branch?"), not a block of keys per repository.
- The VPS is authoritative and the local machine is a working view. The truth
  must live in one place the VPS owns, not in a file copied between machines.

The consumers are unchanged since 0022: `awf-gh` has its own format, the
shell scripts read `tend.list` and `sync.map`, the workflows read `repos.map`
with `awk`. Whatever replaces the file must keep feeding them.

## Decision

We make a database owned by dots the source of truth for projects, work items,
their references, actors, claims and rights. `repos.yaml` stops being the
source. It becomes an import and export format.

**Where it lives.** On the always-on VPS, in the Go dashboard process or a
sibling Go service next to it, the only writer. Other tools read and write
through its loopback API on the tailnet ([0013](0013-dashboard-on-the-tailnet.md)),
never through the database file. A local machine holds no copy of the truth.

**Which store.** We choose SQLite, in WAL mode, one file under `STATE_DIR`.
One process writes, there is one user and a few thousand rows, and the VPS is
small (a few euros a month). SQLite needs no extra service, backs up with a
file copy, and is already the store opencode uses. The Go access sits behind a
small interface in `internal/`, so a move to Postgres changes one package.

**Minimal tables.**

- `actor`: id, kind (`human` or `bot`), display name, and the identity it
  carries per tracker.
- `project`: id, workspace, the repository (`owner/name`), the feature keys of
  0022 as one JSON column (the registered features still validate it),
  checkout path, created and updated times.
- `work_item`: opaque id, project, title, state, created by (`actor`),
  created and updated times.
- `work_item_ref`: work item, reference as `tracker:key` (unique across the
  table, so one reference belongs to one work item, as in 0023), source event,
  attached by (`actor`), time.

Every row that records a change carries an actor id, as decided in 0023.
Claims and the rights table are not designed here (see below).

**Private names stay out of git** ([0003](0003-private-config-not-in-git.md)).
The database file lives under `STATE_DIR`, which is gitignored. Only
`repos.example.yaml` is tracked. Exports go to a gitignored `repos.yaml`.

**Generation.** `bin/repos generate` and `make repos` read the database
instead of the file and write the same four files, with the same headers and
the same write-only-if-changed rule. The consumers do not change.

**Round trip.** `bin/repos import` loads a `repos.yaml` into the database:
projects and features, with defaults and profiles resolved as today. It
upserts by repository and never deletes a project that the file omits, unless
asked with `--prune`. `bin/repos export` writes a `repos.yaml` back, with
`defaults` and `profiles` rebuilt from the stored features. The test of 0022
(regenerate the four files and compare) runs against import then generate,
and a second test runs export then import and compares the database.

**Migration.** In order, each step shippable alone:

1. Add the schema and `bin/repos import`. Keep reading `repos.yaml`.
2. Run both: `generate` reads the database, a check fails when it differs from
   the output of the file.
3. Switch the source to the database. The dashboard edits through the API, and
   the save refusal of 0022 now reads "edit in the dashboard or import a file".
4. Keep `repos.yaml` as an export only, regenerated on change.

Work items are created empty and filled by events. Existing `state/` is not
migrated, as in 0023.

**Left to later records.** Claims and presence (A4) and workspace memory and
its single writer (A3) get their own tables and rules. This record fixes only
that they live in the same database, and that the event bus, chosen
separately, is a notification channel and never a store.

## Consequences

- One authoritative store for projects and work items, safe for several
  writers, with a unique constraint that enforces one work item per reference.
- The generated files and every consumer stay as they are.
- `repos.yaml` is no longer edited as the truth. Hand edits after step 3 are
  lost on the next import unless imported on purpose. The comfort of one
  readable file is partly kept by export.
- A stateful service to back up and migrate. A lost or corrupt database loses
  the work item links. The mitigation is a nightly file copy off the VPS and
  the export, which restores projects but not work items.
- Schema changes need migrations. A new feature key still needs a Go change
  (0022), but its value is stored without a schema change.
- The VPS is now a single point of failure for configuration. Working from the
  local machine alone needs a recent export.

## Alternatives

- **Keep `repos.yaml` as the source and add a second file for work items.**
  No new service, but concurrent writers and relations between rows do not fit
  a file, and the two files would disagree.
- **Postgres.** Handles several writers and growth, and brings a service to run
  and secure for one user and one writer. We keep it as the move if the load or
  the number of writers grows, behind the same interface.
- **A tracker as the store (GitHub Projects, Jira).** It cannot hold work with
  no issue, and it ties the identity to one tracker, which 0023 rejects.
- **A document store or a git repository of JSON files.** Git history is
  attractive, but a lookup by reference needs an index, and concurrent
  writes become merge conflicts.
