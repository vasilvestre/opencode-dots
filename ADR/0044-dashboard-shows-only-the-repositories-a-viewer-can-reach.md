# 0044. The dashboard shows a viewer only the repositories GitHub says they can read

- Status: superseded by 0046
- Date: 2026-11-27
- Supersedes: none
- Superseded by: 0046

## Context

[0037](0037-dashboard-users-and-roles.md) is accepted for its first step, an audit of writes. It left the
question of who may read what for later, and it proposed roles (`owner`, `viewer`) to answer it. The dashboard
shows runs, work items, notes, held records, link proposals and costs of every repository the dot works on, to
anyone who reaches it.

The owner's direction (2026-11-27): if the dashboard requires a GitHub login, it can show each connected person
only the repositories they have permission on, and that is secure by default. GitHub already holds the answer to
"who may see this repository", so the dot would not keep a second list of rights to maintain, and a repository
whose access changes on GitHub changes in the dashboard.

## Decision

Open on the mechanism. The direction is:

- A visitor who is not the host's own user is **not shown anything** until they have authenticated with GitHub.
- Once authenticated, every view that is keyed by repository (runs, work items, notes, held records, link
  proposals, costs, the audit rows that name a repository) is filtered to the repositories where GitHub reports at
  least read permission for that user (`permissions.pull` on `GET /repos/{owner}/{repo}`).
- The check **fails closed**: an API error, an unknown repository or a missing permission hides the repository.
  Results are cached for a short time (about a minute) so a revoked right takes effect quickly.
- Pages that change configuration or release held work stay for the owner only, whatever the viewer can read.
- Reads and writes of the viewer are audited as in 0037, with the GitHub login as the caller.

### Recommendation

Authenticate with the GitHub OAuth **device flow**: the dashboard shows a code, the viewer approves it on
github.com, and the dashboard receives a token that it keeps in memory for the session and never writes to disk
or to a log. It needs an OAuth app registered by the owner (a client id, no secret in the dashboard).

### Options

| Option | Cost | Rules out |
| --- | --- | --- |
| GitHub OAuth device flow (recommended) | the owner registers an OAuth app; one new endpoint pair | typing a password or token into the dashboard |
| The viewer pastes a personal access token | none to set up | it asks people to type a credential into a web page: refused |
| Map the Tailscale identity to a GitHub login in the config | a table to maintain, no GitHub call | checking the permission live, so a revoked right lingers |

### Questions for the owner

1. Is the device flow the right way in, which means registering an OAuth app on your GitHub account or
   organisation?
2. Does the viewer's audit row carry the GitHub login, as proposed?
3. May a repository that GitHub hides from a viewer also be hidden from the costs total, or must the total stay
   complete?

### Settled in discussion (2026-11-27)

- **The device flow is the way in.** The owner registers a GitHub OAuth app with the device flow enabled and gives
  its client id to the dashboard; no secret is stored.
- **The visitor proves who they are, nothing more.** The device flow asks for **no scope**. The dashboard reads the
  visitor's login with it, keeps the login and drops the token at once. A token that could read private
  repositories would be a standing risk if the dashboard were ever compromised.
- **The dot's own `gh` token reads the permission.** For each repository the dashboard asks GitHub for the
  permission of that login (`GET /repos/{owner}/{repo}/collaborators/{login}/permission`) and shows the
  repository only when it is `read` or better. If the dot's token cannot read the permission of a repository, it
  is hidden from every visitor: it fails closed.
- Without a client id the dashboard behaves as today, and the host's own direct loopback requests keep the full view.
- Questions 2 and 3 are taken as proposed: the audit row carries the GitHub login, and a repository hidden from a
  visitor is also left out of the costs total they see.

## Consequences

Easier: no role table to keep in step with GitHub, and a team sees only its own repositories by default. It also
settles the manager and client views of use cases B4 and D4 with no new concept.

Harder: every endpoint that returns repository-keyed data needs the filter, and the dashboard makes GitHub calls
on a viewer's behalf. A token in memory is a secret to protect, and an unavailable GitHub makes the dashboard show
a viewer nothing.

## Alternatives

Roles kept in the dot, as 0037 first proposed: a second list of rights, which drifts from GitHub. No viewer access
at all: safe, and it is what runs today, but it rules out the team and client use cases.
