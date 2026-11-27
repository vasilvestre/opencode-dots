# 0046. The dashboard reads a visitor's rights with the visitor's own token

- Status: accepted
- Date: 2026-11-27
- Supersedes: 0044
- Superseded by: none

## Context

[0044](0044-dashboard-shows-only-the-repositories-a-viewer-can-reach.md) shows a visitor only the repositories
GitHub says they can reach, after a GitHub sign-in by the device flow. It reads the visitor's permission with the
dot's own token and asks the visitor for no scope, so that the dashboard never holds a token able to read code. Two
things make that unfit for a team dot:

- The dot's token is one shared account, and the endpoint it uses (`collaborators/{login}/permission`) only answers
  for a token that can push to the repository. A repository where the dot cannot push is hidden from everyone,
  including the people who work there.
- A team expects each person to bring their own token, and to see what they can use themselves.

The owner's `gh` token carries the `repo` and `read:org` scopes, which is what `gh auth login` asks everyone for.
With such a token GitHub answers "what is my permission on this repository" directly
(`GET /repos/{owner}/{repo}` returns `permissions.pull` and `permissions.push` for the caller).

## Decision

We read a visitor's rights with the visitor's own token, and we show only the repositories the dot works on where
that visitor has at least a configured level.

- **Sign-in.** The device flow asks for the scope `repo`, as `gh auth login` does. The visitor approves it on
  github.com.
- **The token is used once.** Straight after the approval the dashboard reads the visitor's login and, for each
  repository in the dot's own list, the visitor's permission. It then **drops the token**: never stored, never
  logged, never in a cookie.
- **The visible set lives in the session.** The session holds the login, the set of visible repositories and an
  expiry of one hour. A right removed on GitHub disappears at the latest when the session ends. To refresh it, the
  visitor signs in again.
- **A minimum permission, set by the owner.** `min_permission` is `push` by default, so a person sees the
  repositories where they can work. `pull` shows everything they can read, for a client who only consults. A
  repository below the level, unknown to GitHub or unreadable is hidden: the check fails closed, and a failed
  call to GitHub shows the visitor nothing.
- **The dot's own token is no longer needed for visitors.** It stays optional for the owner's own tools.
- **The owner is unchanged.** A request on loopback with no tailnet header keeps the full view, and the pages that
  change anything stay for the owner only.

This replaces the choice of 0044 to ask for no scope and to read the permission with the dot's token. The rest of
0044 stands: the device flow, the filter on every view keyed by repository, the owner-only pages, the audit row
carrying the GitHub login, and no hidden repository showing in counts, totals or errors.

## Consequences

Easier: each person sees exactly what GitHub lets them work on, with nothing to maintain on the dot side; a
repository where the dot cannot push is no longer hidden from the people who can. Moving between `push` and `pull`
is one setting.

Harder: the consent screen asks for `repo`, which is wide. It is the scope `gh auth login` already asks for, and
the dashboard holds the token for the length of one sign-in, but it is a standing reason for a person to hesitate.
Rights are as fresh as the session, one hour at most. A GitHub outage shows visitors nothing.

Rules out: a read-only metadata scope. A GitHub App with metadata permission only would hold less, and it needs an
application installed on the organisation: that option stays open for a later record.

## Alternatives

Keep 0044, with the dot's token: nothing to ask of the visitor, but one shared token and hidden repositories.
A GitHub App with read-only metadata: the strictest, and the most work for an organisation administrator. Keep the
visitor's token for the whole session: fresher rights, at the price of holding a code-capable token for hours.
