# Audit of dashboard writes

ADR 0037 (accepted; roles beyond owner and visitor are not built). Every write the dashboard serves leaves one row in the `audit` table of
the control-plane database (`state/dots.db`, see `docs/store.md`). It records, it never refuses:
there is no role check and no behaviour change.

## What a row holds

| Column | Value |
| --- | --- |
| `at` | the time of this server, never a client's |
| `endpoint` | method and path, for example `PUT /api/save` |
| `target` | a file name or a record id: `workflows/review-pr.yaml`, a held record, `owner/repo#12`, `owner/repo set-key` |
| `caller` | the tailnet login, or `local` |
| `outcome` | the HTTP status the dashboard answered |

No request body, no query string, no header other than the login, no value of a setting. A field
is cut at 200 characters and control characters are dropped. A write that was refused (wrong
origin, bad body, refused path) is a row too, with its 4xx status; it may carry no target when it
was refused before the name was checked.

The table is append only: triggers refuse an `UPDATE` and a `DELETE`.

## Which endpoints

Every handler that is not a read, wrapped in `main.go` by `audited`:

`PUT /api/save`, `PUT /api/models`, `PUT /api/notifications`, `PATCH /api/repos/config`,
`POST /api/autonomy/release`, `POST /api/ask`, `POST /api/ask/answer`, and any non-GET request
through `/awf/` (starting or cancelling a run; the target is the awf path). A new write endpoint
is wrapped the same way, with `auditTarget(r, name)` once the name is validated.

## The caller

`tailscale serve` forwards each request with `Tailscale-User-Login`. The dashboard takes the header
only when the request passes `trustedRequest` (a loopback or `-allow-host` host, and an Origin that
matches): the request the proxy forwards. Any other request is recorded as `local`, whatever header
it sends. Trusting the header is safe only while nothing binds a public interface (`AGENTS.md`).

## Reading it

`GET /api/audit?limit=N` returns the last N entries, newest first (default 100, at most 500), and
answers `[]` when there is no database. It is read only and refuses a host that is not trusted.
The Audit page (`#audit`, key `9`, Ctrl+K) shows the same table.

## When the store fails

The row is written after the handler has answered. If the database cannot take it (locked, full,
unreadable) the failure is logged (`audit: ... not recorded`) and the write stands: the audit never
blocks or fails an endpoint, so a gap in the log is possible and is visible in the dashboard log.

The first write creates `state/dots.db` when it does not exist yet.

## Visitors signed in with GitHub

With GitHub sign-in on (`docs/dashboard-auth.md`, ADR 0046) the caller of a visitor's request is the
GitHub login of their session, and a request that is not signed in is recorded under its tailnet
login, or `anonymous`. Besides writes, the log then holds a completed sign-in, a visitor's refused
read of an owner-only page, and a visitor's reads of data endpoints (one row per login, path and
target a minute). A visitor sees only the rows whose target names a repository they may read.

## Not done

Roles, a `viewer`, logging reads, and an export. See ADR 0037.
