# Refactor progress (branch refactor/entropy)

## Done
- Round 1: four state-file writers (pause record, remote snapshot, pair links, runrecorder index) now use `atomicfile.Write`; guard test `internal/atomicfile/guard_test.go` blocks new bare `os.WriteFile`.
- Round 2: `internal/statedir.Default` is the one STATE_DIR resolution (was five hand-rolled copies in health, dots, dashboard, repos x2).

## Task pool
- `fileExists` and `readCapped` repeated in runlog, runrecorder, spend, dashboard: different caps/semantics, left alone (Deletion Test: a shared helper would not hide complexity).
- `clip` x3 (sentry, jira, store/audit): different return shapes, left alone.
- Not yet scanned: dashboard handler error shapes (http.Error vs writeJSON), `os.UserHomeDir` x6, time.Parse RFC3339 x11, logging (log vs fmt to stderr).

## Needs decision
- none
