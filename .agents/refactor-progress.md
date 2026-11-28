# Refactor progress (branch refactor/entropy)

## Done
- Round 1: four state-file writers (pause record, remote snapshot, pair links, runrecorder index) now use `atomicfile.Write`; guard test `internal/atomicfile/guard_test.go` blocks new bare `os.WriteFile`.

## Task pool
- `fileExists` and `readCapped` repeated in runlog, runrecorder, spend, dashboard: different caps/semantics, left alone (Deletion Test: a shared helper would not hide complexity).
- `clip` x3 (sentry, jira, store/audit): different return shapes, left alone.
- Scan not yet done: dashboard handlers error shape, env access (`envOr` only in cmd/dashboard), logging.

## Needs decision
- none
