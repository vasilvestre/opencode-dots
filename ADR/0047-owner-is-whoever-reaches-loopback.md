# 0047. The owner is whoever reaches the dashboard on loopback; an identity-based owner waits

- Status: accepted
- Date: 2026-11-27
- Supersedes: none
- Superseded by: none

## Context

The owner view of the dashboard (config, models, files, ask, links, repository settings) sits behind `ownerOnly`.
`ownerRequest` in `cmd/dashboard/auth.go` grants it to a request that reaches the dashboard directly on loopback:
Host and source IP are loopback, and no identity header set by a proxy is present. Anything a reverse proxy forwards
(`tailscale serve` today, another overlay network tomorrow) is a visitor, signs in with GitHub
([0046](0046-the-visitor-brings-their-own-token.md)) and sees no owner pages, even when that visitor is the owner.

On the scaleway server this means nobody gets the owner view without an SSH tunnel to `localhost:8099`. That is
acceptable today: whoever holds the SSH access can already read `state/`, `.env` and the tokens, so the owner view
gives them nothing more. Two limits are known:

- The model does not tell people apart. A second SSH access (a collaborator, a service account) is an owner, and the
  audit trail can only say "local caller".
- The owner view is out of reach without a tunnel, for example from a phone.

The network may change: replacing Tailscale with NetBird has been discussed. A way to recognise the owner must not
depend on one overlay network, so the proxy header name `Tailscale-User-Login` must not become part of the model.

## Decision

We keep the owner defined as a direct loopback request, and we do not add an identity-based owner yet.

When the need appears, the extension must be provider-neutral and off by default. Two mechanisms are allowed, each
enabled on its own:

- **GitHub login.** `OWNER_GITHUB_LOGINS` lists logins; a session from the sign-in of 0046 with one of them gets the
  owner role. It does not depend on the network. Direct loopback may then be turned off, so that SSH alone no longer
  gives the owner view.
- **Proxy identity header.** `TRUSTED_PROXIES` (CIDRs, loopback by default), `OWNER_IDENTITY_HEADER` (a name, never
  hard-coded) and `OWNER_IDENTITIES` (accepted values). The header is read only when the source IP is in
  `TRUSTED_PROXIES`. Whether NetBird's proxy sets such a header is not established and must be checked first.

With the variables empty, behaviour is the current one.

We revisit when one of these becomes true: the SSH access is shared with someone who must not be an owner, the owner
view is needed without a tunnel, or the audit trail must name who acted.

## Consequences

- A developer who reaches the dashboard through the team listener (`-team-addr`, docs/team-ui.md) is a visitor,
  not an owner: only a direct loopback request is the owner, so SSH access to the server is owner access and
  SSH access to the team port is not.
- No code change now, and the security posture stays simple to state: SSH access is owner access.
- A tailnet visitor, the owner included, stays a visitor until this is revisited.
- The extension, when built, costs a new trust input (a GitHub account becomes owner-grade, so its 2FA and keys
  matter) and a rename of `tailscaleLoginHeader` in `auth.go`.
- Moving to NetBird needs no change to the owner model.

## Alternatives

- **Header-based owner for Tailscale only.** Rejected: it ties the model to one provider that may be replaced.
- **GitHub login as owner now.** Deferred, not rejected: it raises the owner view's security to that of a GitHub
  account, for a need that does not exist while SSH is the owner's alone.
- **Sign-in even on loopback, to name the caller in the audit trail.** Deferred: it adds friction for the one case
  that works today.
