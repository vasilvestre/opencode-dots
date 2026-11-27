# Work sources

`internal/sources` is the inbound half of the adapters of ADR 0023 and ADR 0039. It is
a package and its tests; only the advisory adapter is called, by `bin/advisories` (`docs/advisories.md`).

## Envelope

Every adapter turns a raw event into `Envelope` values:

| Field | Meaning |
| --- | --- |
| `ref` | the work, as `tracker:key` |
| `kind` | what happened, owned by the adapter (`advisory.reported`) |
| `actor` | who or what caused it, never empty |
| `time` | when the source says it happened |
| `payload` | the source's own fields, as JSON |
| `key` | idempotency key, stable across polls |

`Adapter` has two methods, `Tracker()` and `Parse(raw)`. Outbound writes stay in shell
steps (ADR 0002). `Parse` may return valid envelopes together with an error that names
what it left out.

## References

| Tracker | Key | Example |
| --- | --- | --- |
| `github` | `owner/repo#N` | `github:acme/shop#12` |
| `branch` | `owner/repo:name` | `branch:acme/shop:fix/login` |
| `jira` | `ABC-12` | `jira:ABC-12` |
| `sentry` | `org/project#id` | `sentry:acme/web#4711` |
| `advisory` | `GHSA-xxxx-xxxx-xxxx` or `CVE-YYYY-NNNN`, then `@owner/repo` | `advisory:GHSA-cfgh-2345-wxvr@acme/app` |

`ParseRef` validates the shape and never looks the tracker up. These strings are what
`internal/store` keeps as references; `store` only checks `tracker:key`, this grammar is
stricter. A reference is scoped when the thing it names is the same in many places (an
advisory hits many repositories); the old unscoped `advisory:<id>` is rejected by `ParseRef`, and
`store.Sourced.LegacyRef` lets a stored item with it be found and rewritten for its own project.

## Idempotency

`IdempotencyKey(parts...)` hashes length-prefixed parts. Use what makes the event what it
is (reference, kind, the source's event id) and never a time that changes between polls.
Passing the key to the store as the source event makes a repeated poll a no-op.

## Advisory adapter

`internal/sources/advisory` reads `composer audit --format=json` for one repository (`Adapter.Repo`,
required). It emits one envelope per advisory id, referenced as `advisory:<id>@<owner>/<repo>` so the
same advisory in two repositories is two work items and the key stays stable per repository, with every affected package in the payload (name, affected range,
fixed versions taken from the upper bounds), the CVE ids and a severity (`low`, `medium`,
`high`, `critical`, `unknown`). The id comes from a GitHub source (`GHSA-`) or the `cve`
field. An entry with neither is left out and reported in the error. Titles and links are
carried as data and never read for references or commands.

## Wired end to end

The advisory source is wired: `bin/advisories` feeds the adapter, `bin/repos db ingest` records the
work items, and a classification and a note follow. See `docs/advisories.md`. Jira is wired as far as the work item: `bin/jira-poll`
(`docs/sources-tracker.md`). Sentry is wired the same way: `bin/sentry-poll` (`docs/sources-sentry.md`).
