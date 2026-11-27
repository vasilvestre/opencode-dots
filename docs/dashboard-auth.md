# Dashboard sign-in with GitHub

ADR 0044 and ADR 0046. A person who reaches the dashboard through the tailnet signs in with GitHub and
sees only the repositories where GitHub gives them at least the configured permission (`push` by default). It is off unless you give the dashboard a
GitHub OAuth client id; with it off the dashboard behaves exactly as before.

## Turn it on

1. On GitHub: Settings, Developer settings, OAuth Apps, New OAuth App (on your account or on an
   organisation). Any name and any homepage URL. Tick **Enable Device Flow**. The callback URL is
   not used by the device flow; put the dashboard address there. No client secret is needed and the
   dashboard never asks for one.
2. Put the client id in the dashboard unit's environment, for example in
   `systemd/dots-dashboard.env`:

   ```
   DOTS_GITHUB_CLIENT_ID=<the client id>
   DOTS_MIN_PERMISSION=push
   ```

   The dot needs no GitHub token of its own for visitors: each visitor brings theirs. `GH_TOKEN` is
   not read by the dashboard any more.
3. Restart the dashboard (`systemctl --user restart dots-dashboard.service`).
4. Keep `DOTS_ALLOW_HOST` set to the tailnet name: a visitor's request must come from a trusted host.

The flags are `-github-client-id`, `-github-url` (default `https://github.com`) and `-github-api`
(default `https://api.github.com`); the environment names are `DOTS_GITHUB_CLIENT_ID`,
`DOTS_GITHUB_URL` and `DOTS_GITHUB_API_URL`. The last two exist for tests.

`-min-permission` (`DOTS_MIN_PERMISSION`) is `push` (the default) or `pull`; any other value stops the
dashboard at start with a message. With `push` a visitor sees a repository when their permission is
write, maintain or admin; with `pull`, when it is read, triage, write, maintain or admin (triage
counts as read). The sign-in page says which one is in force ("you see the repositories where you can
push").

## Who is the owner

The owner is the host's own user: a request that reaches the dashboard directly on loopback
(`127.0.0.1`, `localhost`, `[::1]` as the `Host`, from a loopback address) with no
`Tailscale-User-Login` header. The owner sees everything and never signs in.

Every other request is a visitor: one that carries the header, or one whose `Host` is not loopback,
which is what `tailscale serve` forwards. A visitor sees nothing until they sign in.

A request that arrives on the **team listener** (`-team-addr`, `DOTS_TEAM_ADDR`) is a visitor too, whatever
its address, `Host` or headers: an SSH tunnel ends on loopback, where the owner is. See
[team-ui.md](team-ui.md).

## Signing in

The page shows a sign-in button. It asks GitHub for a code (the OAuth **device flow**, scope `repo`),
shows it with a link to github.com, and polls until the visitor approves.

**The scope.** `repo` is wide: it is the scope `gh auth login` asks everyone for, and it is the one that
lets GitHub answer "what is my permission on this repository" for private repositories. It is
acceptable because the dashboard uses the token **once**, inside the poll that sees the approval: it
reads the login (`GET /user`) and, for every repository in the dot's own list, the visitor's
permission (one GraphQL call with `viewerPermission` per 50 repositories; if GraphQL fails, `GET
/repos/{owner}/{repo}` for each one). Then it drops the token. It is never stored in the session, a
cache, a log, an error, an audit row or a cookie. A GitHub App with metadata permission only would hold
less; it is a later option (ADR 0046).

**The session.** The dashboard keeps the login, the set of visible repositories, an
expiry of **one hour** and a random 256-bit id sent as an `HttpOnly`, `SameSite=Strict` cookie
(`Secure` when the request came over https). Rights are as fresh as the session: a right removed on
GitHub disappears at the latest when it ends, and signing in again refreshes it. "Sign out" in the
nav ends it at once.

**Sessions survive a restart.** They are written to `STATE_DIR/sessions.json` (mode 0600, the directory
0700), atomically (temp file in the same directory, fsync, rename), about a second after a sign-in, a
logout or an expiry, so a deploy does not sign visitors out. The file is keyed by the SHA-256 of the cookie
id, so it cannot sign anyone in. Each entry holds only the login, the visible repositories, the expiry and
the creation time. **Never written:** the cookie id itself, any GitHub token (the dashboard drops it
at sign-in), the device code and the user code. At start the file is read and expired entries dropped; a
missing, empty or corrupted file, or one wider than 0600, is ignored with a warning (no session content in
it) and left in place. The store holds 1000 sessions at most, dropping the oldest. A failed write never
breaks sign-in: the sessions stay in memory and one warning a minute is logged. With sign-in off no file is
created or read. **To sign everyone out:** delete `sessions.json` and restart the dashboard.

Endpoints: `POST /auth/device/start`, `POST /auth/device/poll`, `POST /auth/logout`, `GET /api/session`.
The first three are rate limited per client and check the host, the origin, a JSON content type and a
4 KiB body, like the other writes. The poll honours GitHub's `interval` and `slow_down`; the code
expires with GitHub's `expires_in`.

## What a visitor sees

A repository of the dot's list is shown when the visitor's permission reaches `min_permission` (above),
as computed at sign-in. A repository GitHub does not return, a 403 or 404, and any error hide it: the
check fails closed, and a failure of GitHub shows the visitor nothing. Authorization is a lookup in the
session's set; the dashboard does not call GitHub again until the visitor signs in again.

Filtered to those repositories: artifacts (reviews, replies, seen), a run's log, stream, price,
steps, sessions and replay, `/api/logs`, `/api/usage` (the total, the run count and the unpriced
count are added up again from the visible groups), `/api/home` (the Home page: one row per visible
repository, each with numbers of that repository alone), executors' runs, repos and repo names, memory,
claims, link proposals (only when both items are visible), autonomy notes and held records and their
artifacts, Ask threads, artifact files, and audit rows whose target names a repository.

A record of a hidden repository answers like one that does not exist (404, the same body), appears in
no list, count, total or error message. A run whose repository is not known is hidden.

## What a visitor sees

A visitor lands on **Home**: a table of the repositories of their session (the ones the dot knows that
they may see, plus any of their session the dot has recorded nothing about), each with the latest
recorded activity, the runs and cost of the last 7 days, the held records and notes, and a link to the
repository's Memory entry. Every number is computed per repository, so a hidden repository is in no row
and no count. The owner lands on the same page, with every repository the dot knows. Cost, Memory,
Autonomy, Audit and Links follow, filtered as above.

The repository of a run is found from, in order: the `repo` input awf recorded for the run (its state
file), a line the workflow's own prompt template prints at the head of the first user message (`repo:
owner/name` as the first line, or the opening sentence of `tend-repo`), then the `owner/repo#N` or
`issue #N` guess from the prompt. Text a pull request or an issue could carry is never read for the
first two. So the daily pass (`tend-repo`) shows on Cost under its repository, not under "(no pull
request)". A run whose repository cannot be found stays unattributed and hidden from visitors.

## What is owner only

A visitor gets 403 here even when signed in, and 401 when not signed in: `/api/save`, `/api/source`,
`/api/files`, `/api/models`, `/api/notifications`, `/api/repos/config`, `/api/autonomy/release`,
`/api/links/accept`, `/api/links/decline`, `/api/links/split`, `/api/ask`, `/api/ask/answer`,
`/api/workers`, `/api/health` and the `/awf/` proxy. The page hides the Activity, Workflows, Settings,
Repos (the `repos.yaml` editor) and Health sections from a visitor, who lands on Home. Activity is built
on awf's own history, which is the owner's.

## Audit

A visitor's request is recorded with the GitHub login as the caller: a refused write or a refused read
of an owner-only page, a completed sign-in, and each read of a data endpoint (one row per login, path
and target a minute, so a page that polls does not flood the table). See `docs/audit.md`.

## Limits

- Rights are read at sign-in: a revoked right takes up to one hour to show, and a new right needs a new
  sign-in.
- The consent screen asks for `repo`, which some people will hesitate over.
- The list of repositories is the dot's own; a run or a record of a repository outside it is hidden
  from visitors.
- No organisation-level rules: the answer is the visitor's own permission on the repository.
- Sessions survive a restart through `sessions.json`; a visible set changed on GitHub still shows until the hour ends.
- The Activity page does not work for a visitor yet; runs show on the Cost page and through
  `/api/logs`.
- A run with no repository in its recorded inputs or in the head of its first prompt stays hidden from
  visitors, even when it is the dot's own.

## What was checked

Before ADR 0046 (the flow without a scope and the dot's token), on 2026-11-27, against a fake GitHub (nothing real was contacted), with the merged code and a scratch state:

- **In a browser, as a visitor** (`box.localhost`, so not loopback): the sign-in page, the device code and the
  "Open GitHub" link, the automatic move to the landing page once approved (Cost then, Home now), and an Autonomy page that listed the note of
  the readable repository and not the one the visitor has no permission on.
- **With curl:** the owner (direct loopback, no tailnet header) sees both notes; a visitor with no session gets 401;
  a signed-in visitor gets 403 on an owner-only endpoint; the visitor's token appears in no file, log or cookie.
- **After a restart of the dashboard** (at that time): the session was gone, since sessions lived in memory only. They are
  now kept in `sessions.json` and survive a restart (see above).

Not checked: the real GitHub, where the response shapes follow the documentation, and the Activity page, which a
visitor does not get.

