# Pickup: a visible "a dot took this"

When `review-pr` starts on a pull request, `bin/pickup start` puts the label `dots:working`
and an eyes reaction on it. When the run ends, by any path, `bin/pickup stop` removes the
label. Both are never fatal, and `autonomy: observe` writes nothing.

It is a signal for people. It is not a lock and no dot reads it (ADR 0045: dots do not talk).
A branch veto is still `bin/claim` (ADR 0026).

## Who took it

On a team's server the watcher queues each event and a worker claims it ([remote.md](remote.md),
[executors.md](executors.md)): the claim is what says which machine took the work, and the dashboard's Activity
view shows the worker. The label is for the people looking at the pull request.

Only `review-pr` sets the label today; `reply-pr`, `fix-issue` and `fix-ci` do not yet.

Test: `bin/test-pickup`.
