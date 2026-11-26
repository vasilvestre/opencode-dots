# 0037. The dashboard has users, roles and an audit trail

- Status: proposed
- Date: 2026-11-26
- Supersedes: none
- Superseded by: none

## Context

The dashboard answers on the tailnet through `tailscale serve`
([0013](0013-dashboard-on-the-tailnet.md)) and has one level of access: whoever reaches it can
read runs and edit configuration ([0017](0017-dashboard-edits-completion-notifications.md)).
That is right for one owner. A team needs a manager who sees what shipped without code, an
observer who cannot edit, an admin who holds keys, and a client who sees only their own
repository. Companies also ask for a record of who changed what. See use cases B4, D2, D4, D5.

## Decision

Open. Proposed shape: identity comes from the network or an SSO in front of the dashboard (the
tailnet already names the caller), roles are `owner`, `admin`, `member`, `viewer`, and a role
is scoped to a workspace or repository, never global. Every edit and every read of a run's
content is written to an append-only log in the control-plane database.

## Consequences

Makes a view per role and a client view possible, and gives D2 its audit. Costs a permission
check on each endpoint of `cmd/dashboard` and a login story that works on loopback, tailnet and
behind a company bastion. Rules out leaving the dashboard single-user while claiming team
support.

## Alternatives

One dashboard per user: no shared view, no manager view. Putting access control only in the
reverse proxy: coarse, and it cannot tell a read of a run from an edit of configuration.
