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

## Work items from GitHub events

`internal/workitem` is the GitHub adapter of ADR 0023. It reads structured fields only (the number,
the head branch, the issues GitHub itself links to a pull request) and never parses a title or a body.

```bash
bin/repos db observe -repo owner/name -kind pr-opened -number 14 -branch fix-login -actor ada -title "Fix the login"
bin/repos db observe -repo owner/name -kind pr-opened -number 14 -linked 12 -branch fix-login -actor ada
bin/repos db work-items [-repo owner/name]
```

An event creates a work item when none of its references is known, attaches the missing references
to the one work item that holds any of them, and fails with a conflict, attaching nothing, when they
belong to two (a merge is explicit, ADR 0023). Observing the same event again changes nothing.

`bin/claim` calls `observe` when a claim starts, and the source adapters do when they ingest
(`bin/repos db ingest`, docs/sources.md). `awf-gh` is a separate project and does not, so a workflow's shell step is the
place to call it where an event should become a work item.

## Generating from the database

`bin/repos generate -from-db` and `bin/repos check -from-db` build the generated files from the
database instead of `repos.yaml` (step 2 of the migration). Without the flag nothing changes.

## The schema, migration by migration

`schema_version` holds one row per applied migration. Each migration runs in its own
transaction and is never edited once shipped: a change is a new entry in `internal/store/store.go`.
A database newer than the build is refused. Times are RFC 3339 UTC, the server's own clock.

| Version | Adds | Where it is described |
| --- | --- | --- |
| 1 | `workspace`, `actor`, `project`, `work_item`, `work_item_ref` | above |
| 2 | `claim` | `docs/claims.md` |
| 3 | `audit` | below, `docs/audit.md` |
| 4 | `worker` | below, `docs/workers.md` |
| 5 | `work_item.payload`, `work_item_event` | below |
| 6 | `link_proposal`, `work_item.merged_into` | below, `docs/links.md` |
| 7 | `link_proposal` rebuilt with the `split` state, `split_by`, `split_at` | below, `docs/links.md` |

### 3. `audit` (ADR 0037)

The write log of the dashboard. Columns: `id` (integer, autoincrement), `at`, `endpoint` (method
and path), `target` (a file name or a record id, never a body), `caller`, `outcome` (the HTTP
status). It is append only: two triggers, `audit_no_update` and `audit_no_delete`, abort an
`UPDATE` or a `DELETE`, so a bug cannot rewrite history. `RecordAudit` needs an endpoint and a
caller, strips control characters and cuts every text field to 200 bytes. `RecentAudit` lists
newest first, 100 by default and 500 at most.

### 4. `worker` (ADR 0042)

Presence only: a machine that offers its capacity, with no task sent to it. Columns: `name`
(primary key), `actor_id` (a `bot` actor made for it), `machine`, `capabilities` (a JSON object,
`{}` by default), `registered_at`, `renewed_at`, `expires_at`. Registering a name twice is one
worker: it renews, and keeps the registration time while the earlier one is still live. A worker
is live while `expires_at` is in the future; the time to live is 90 seconds by default and an
hour at most, as for a claim. A heartbeat on an expired worker fails with `ErrExpired`: it
registers again. Expiring by hand sets `expires_at` to now. A name has no spaces and a machine is
required.

### 5. `work_item.payload` and `work_item_event` (ADR 0039)

What a source says about a work item. Both are data from outside, never instructions.

- `work_item.payload`: the latest payload, a JSON text, `{}` by default. `RecordSourced` is the
  only writer: it creates the work item for a reference the first time, finds it again after, and
  compares the compacted payload. The same payload writes nothing, a different one replaces the
  stored one and records an event holding the previous and the new payload. A reference held by a
  work item of another project fails with `ErrWrongProject`, and nothing is moved. A reference is
  never taken from the payload or the title.
- `work_item_event`: the trail of a work item, appended only by use (no code updates or deletes a
  row, though there is no trigger). Columns: `id` (integer, autoincrement), `work_item_id` (cascades
  with the item), `at`, `kind`, `actor_id`, `data` (JSON). Index `work_item_event_item` on
  `(work_item_id, id)`. The kinds written so far: `<kind>.created`, `<kind>.changed` and
  `<kind>.ref_migrated` from a source (`advisory`), and the `link.*` kinds below.

### 6. `link_proposal` and `work_item.merged_into` (ADR 0041)

- `work_item.merged_into`: the id of the work item that took this one's references, `''` while it
  is not merged. A merged item has the state `merged`.
- `link_proposal`: `id` (`lp_` and 128 random bits), `work_item_a`, `work_item_b` (both cascade),
  `confidence` (a number from 0 to 1, enforced by a CHECK), `reason`, `evidence` (a JSON object),
  `state`, `proposed_by`, `created_at`, `decided_by`, `decided_at`. The two ids must differ
  (CHECK). A unique index on the smaller and the larger id, only while the state is `open`, makes a
  pair unique in either order while it waits and lets it be proposed again once decided.

The rules are in `docs/links.md`: `reason` is cut to one paragraph of 500 characters, the evidence
is at most 8 KiB, and accepting moves references in one transaction.

### 7. `link_proposal` rebuilt (ADR 0041, split)

SQLite cannot widen a CHECK, so the migration creates the table again with the state
`open`, `accepted`, `declined` or `split`, copies every row, drops the old table, renames the new
one and creates the open-pair index again. Nothing references `link_proposal`, so no other table
changes. It adds `split_by` and `split_at` (both `''` until a split). Existing rows keep every
value.
