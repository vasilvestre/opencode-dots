# 0037. The dashboard has users, roles and an audit trail

- Status: proposed
- Date: 2026-11-26
- Supersedes: none
- Superseded by: none

## Context

The dashboard answers on the tailnet through `tailscale serve`
([0013](0013-dashboard-on-the-tailnet.md)) and has one level of access: whoever reaches it can
read runs and edit configuration. `cmd/dashboard/AGENTS.md` lists the routes: reads
(`/api/log`, `/api/file`, `/api/memory`), writes (`/api/save`, `/api/models` PUT,
`/api/notifications` PUT) and one action that starts work (`/api/autonomy/release`, which starts
`publish-held`). `cmd/dashboard/security.go` only checks that Host and Origin are this machine
(`loopbackRequest`). It does not know who the caller is.

What is built: nothing on users. The store has an `actor` table with `human` and `bot` kinds
(`internal/store/store.go`), but no role, no session and no audit table. The write endpoints share
one lock, which is a natural place to log.

A team needs a manager who sees what shipped without code, an observer who cannot edit, an admin
who holds keys, and a client who sees only their own repository. Companies also ask for a record of
who changed what. See use cases B4, D2, D4, D5.

## Decision

Open. Proposed shape: identity comes from the network or an SSO in front of the dashboard (the
tailnet already names the caller), roles are `owner`, `admin`, `member`, `viewer`, and a role is
scoped to a workspace or repository, never global. Every edit and every read of a run's content is
written to an append-only log in the control-plane database.

### Recommendation

Do the audit first, roles second. Audit needs no identity decision and is useful for one owner.

Smallest first step: an append-only `audit` table and one call at each write endpoint
(`/api/save`, `/api/models`, `/api/notifications`, `/api/autonomy/release`), recording time,
endpoint, target and the caller as the header `Tailscale-User-Login` when `tailscale serve` sets
it, else `local`. No role check yet. Once the log shows real callers, add a two-role rule:
`owner` (today's behaviour) and `viewer` (GET only), enforced in one middleware.

### Options

| Option | What it costs | What it rules out |
| --- | --- | --- |
| A. Audit log of writes, caller from the tailnet header (recommended) | one table, four call sites | logging reads; real roles for now |
| B. A plus `owner` and `viewer` roles in one middleware | A plus a login story on loopback | scoped roles, client views |
| C. Full roles scoped to workspace and repository | a permission check on every endpoint | shipping soon |
| D. Proxy-only access control | nothing in the code | telling a read from an edit; any audit |

### Questions for the owner

1. Is the `Tailscale-User-Login` header an acceptable source of identity (yes, or pick: a reverse
   proxy in front instead)?
2. Is an audit of writes only enough for now, without logging reads of run content (yes or no)?
3. Does a viewer role need to exist before the first teammate does (yes or no)?

### First step built

The audit exists and nothing else: an append-only `audit` table in the control-plane database, one
row for every write the dashboard serves (the five write endpoints and a write through the awf
proxy), and a read-only `GET /api/audit` with an Audit page. The caller is `Tailscale-User-Login`
only on a request that passes the same trust check as a write, else `local`. There is no role
check and no behaviour change; a store that cannot take the row never blocks the write. This ADR
stays proposed: roles, the questions above and the login story are still open. See `docs/audit.md`.

## Consequences

Makes a view per role and a client view possible, and gives D2 its audit. Costs a permission check
on each endpoint of `cmd/dashboard` and a login story that works on loopback, tailnet and behind a
company bastion. Trusting a header is safe only because nothing binds a public interface
(`AGENTS.md`); if that ever changes, the header must be verified. Rules out leaving the dashboard
single-user while claiming team support.

## Alternatives

One dashboard per user: no shared view, no manager view. Putting access control only in the
reverse proxy: coarse, and it cannot tell a read of a run from an edit of configuration.
