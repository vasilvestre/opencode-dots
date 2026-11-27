# Pickup: a visible "a dot took this"

When `review-pr` starts on a pull request, `bin/pickup start` puts the label `dots:working`
and an eyes reaction on it. When the run ends, by any path, `bin/pickup stop` removes the
label. Both are never fatal, and `autonomy: observe` writes nothing.

It is a signal for people. It is not a lock and no dot reads it (ADR 0045: dots do not talk).
A branch veto is still `bin/claim` (ADR 0026).

## Task on one machine, watch on another (ADR 0031)

The server watches GitHub and sends each event to the `awf serve` of the machine named by the
repository's `executor`. From the workstation:

```bash
make executor-tunnel SERVER=user@host    # server 127.0.0.1:2512 -> local awf serve 2511
```

On the server `repos.yaml` has `settings.executors.workstation: http://127.0.0.1:2512` and the
repository says `executor: workstation`. With the tunnel closed the event is not marked seen,
so it is retried on the next pass.

Test: `bin/test-pickup`.
