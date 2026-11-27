# 0051. Read access shows repository names

- Status: proposed
- Date: 2026-11-27
- Supersedes: 0046
- Superseded by: none

Clarifies the visitor model in
[0046](0046-the-visitor-brings-their-own-token.md) by splitting repository names
from detailed dashboard data. It does not change the owner model in
[0047](0047-owner-is-whoever-reaches-loopback.md).

## Context

ADR 0046 gives each visitor one repository set. `DOTS_MIN_PERMISSION` controls
that set and defaults to `push`. A read-only collaborator therefore sees no
repository, even when GitHub confirms read access.

Repository read access does not imply permission to read the dot's operational
data. That data includes runs, costs, logs, memory, notes, claims, artifacts,
audit targets, and hub topics. Opening the whole dashboard to read-only users
would expose more than repository names.

GitHub also separates collaborator permission from OAuth app authorization. An
organization can require owner approval for an OAuth app. SAML SSO can require
a separate authorization. A repository admin can still receive `NOT_FOUND` from
GraphQL and `403` from REST when the OAuth token lacks organization access.

The current dashboard discards these failures. A visitor then sees an empty
Home page that looks like successful zero access. The dashboard must explain an
empty result without revealing hidden repository names or claiming one exact
cause.

## Decision

We split visitor repository visibility into two tiers. `readable` contains
repositories whose names and presence a visitor may see, using
`DOTS_MIN_PERMISSION` with `pull` as the default. `visible` contains repositories
whose detailed dashboard data a visitor may see and always requires `push`. We
compute both sets from one GitHub permission check, store both in the session,
and keep `visible` as a subset of `readable`. We build read-only Home rows from
an explicit allowlist, use `readable` only for repository names and presence,
and use `visible` for every detailed endpoint. Failed permission checks enter
neither set. ADR 0047 remains unchanged.

## Consequences

- Read, triage, write, maintain, and admin permission meet the default name
  threshold.
- Only write, maintain, and admin permission meet the detail threshold.
- `DOTS_MIN_PERMISSION=push` makes both tiers push-only.
- `DOTS_MIN_PERMISSION=pull` shows names to read-only visitors and keeps details
  push-only.
- A readable-only Home row contains only the repository name and a marker that
  details require push access. The marker also tells the visitor to sign in
  again to refresh access.
- Detailed lists, totals, files, logs, costs, memory, claims, artifacts, audit
  rows, links, and hub topics continue to exclude readable-only repositories.
- A detailed request for a readable-only repository returns the same response as
  a missing repository.
- Old sessions without a `readable` field keep their stored names but lose
  detailed access until the visitor signs in again.
- Outstanding hub subscriber tokens remain valid until their 15-minute expiry.
  An immediate cutoff requires subscriber signing-key rotation.
- New sessions persist both sets. The visitor token remains one-use and is not
  retained.
- OAuth organization approval and SAML authorization still gate both tiers.
- Home explains an empty visitor result without naming hidden repositories,
  counting them, or treating every failure as an OAuth denial.
- Owner access and owner-only writes remain unchanged.

## Alternatives

- **Use one `pull` set for everything.** Rejected because read-only users would
  gain operational data that their GitHub role does not grant.
- **Keep one `push` set and improve only the empty message.** Rejected because
  read-only collaborators would still not see repository names.
- **Fetch read-level names on demand.** Rejected because it requires retaining
  the visitor token or adding a second authorization mechanism.
- **Show every configured repository name.** Rejected because it reveals
  repositories the visitor cannot access.
- **Invalidate every old session.** Rejected because an additive migration can
  preserve names while failing closed for details.

Next:

```bash
bin/adr check
```
