# 0005. Run cost comes from opencode's database

- Status: accepted
- Date: 2026-11-23
- Supersedes: none
- Superseded by: [0008](0008-run-cost-comes-from-awf.md)

## Context

Each workflow run spends agent time, and the page shows runs but not what they
cost. awf parses the price out of opencode's stream (`part.cost` on
`step_finish`), then drops it: the agent result and the run events carry no
cost, and neither the transcript nor the recorded log keeps it. So the price
cannot come from anything awf exposes.

opencode keeps its own record: `opencode.db`, table `session_v2` (older
releases: `session`), with `cost`, token counts, `directory` and
`time_created` per session. awf opens one session per agent step, in the
working directory `awf serve` runs from, which is `state/`.

The page already reads `state/` and proxies awf. Reading a second local
database adds a data source and a runtime dependency, so it needs a record.

## Decision

We read opencode's database read-only and show the price. `/api/cost` sums the
sessions of one run; `/api/costs` lists the sessions in a window and the page
attributes each to one history row. Attribution matches sessions by their
directory (`state/` and its subdirectories, never the repository root, which
also holds interactive sessions) and by the run's time window, giving an
ambiguous session to the longer run.

Both handlers exist regardless of the database. Without the `sqlite3` binary,
without the database, or on any query error, they answer
`{"available": false}` and the page shows no price. The query runs through the
`sqlite3` command line, so the dashboard stays a single static Go binary with
one dependency; the Docker image installs that binary.

## Consequences

A run's price, its token counts and the price of a whole history page are one
click or one refresh away. The page now reads a 9 GB database that another
process writes; it opens it in read-only mode and never blocks the writer.

The number is an estimate by construction: a session is matched by time and
working directory, not by an execution id, because awf passes none to opencode.
A workflow that runs the agent in a `dir:` outside `state/` shows no price, and
two runs whose windows overlap can in principle be mis-attributed. The dashboard
must never run as root or expose the database.

## Alternatives

- Ask awf to expose the cost. That means a patch or an upstream change; the
  dashboard should not wait for it.
- Add a Go SQLite driver (for example `modernc.org/sqlite`). It removes the
  binary dependency but grows the build and the binary by megabytes for one
  query.
- Read opencode's session files instead of the database. Newer releases keep
  the data in the database only.
