# 0008. Run cost comes from awf

- Status: accepted
- Date: 2026-11-23
- Supersedes: [0005](0005-run-cost-from-opencode-db.md)
- Superseded by: none

## Context

Record 0005 read prices out of opencode's SQLite database, because awf parsed
the cost of an agent step and then dropped it. That decision carried three
costs of its own: the dashboard forked the `sqlite3` binary on every refresh
over a nine-gigabyte database, it had to guess which run owned which session
from time windows, and a refresh could show a price that belonged to another
row.

awf v0.12.0 (2026-11-23) records the price with the run: `cost_usd` on each
agent `step.completed` and `step.failed` event, in the transcript and on the
SSE stream.

## Decision

The dashboard reads the price from awf's own run log and sums it per run. It
opens no other database, ships no `sqlite3`, and owns no attribution window.

- `/api/cost?id=` sums `cost_usd` over the run's transcript, with the recorder
  file (`state/logs/<id>.jsonl`) as the fallback.
- `/api/logs` carries the same number per row, so the history table fills with
  one request.
- `/api/costs` and the whole opencode-database path are deleted.
- The sum is memoised per run file, keyed by size and modification time.
- A run started before v0.12.0 has no price. The page shows none instead of
  inventing one, and the missing price disappears as the old logs age out.

## Consequences

The dashboard depends on an awf that records costs. With an older awf installed
it still runs: it simply shows no price. The Dockerfile pins `AWF_REF=v0.12.0`
for that reason, and a local install must be at least that version.

What a run spent is now the same number awf reported for that run, not a sum of
sessions that happened to overlap it, so the history table no longer has to
explain how a price was attributed.
