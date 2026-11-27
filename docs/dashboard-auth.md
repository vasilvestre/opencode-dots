# Dashboard sign-in with GitHub

ADR 0044, first step. A person who reaches the dashboard through the tailnet signs in with GitHub and
sees only the repositories GitHub says they can read. It is off unless you give the dashboard a
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
   GH_TOKEN=<the dot's own token>
   ```

   The dashboard reads the dot's token from `GH_TOKEN` (it holds no other GitHub access). It uses it
   only to ask GitHub what a login may read, never to write, and never prints or logs it. The token
   needs read access to the repositories' collaborator permissions: GitHub answers that call only
   for a token that has push access to the repository.
3. Restart the dashboard (`systemctl --user restart dots-dashboard.service`).
4. Keep `DOTS_ALLOW_HOST` set to the tailnet name: a visitor's request must come from a trusted host.

The flags are `-github-client-id`, `-github-url` (default `https://github.com`) and `-github-api`
(default `https://api.github.com`); the environment names are `DOTS_GITHUB_CLIENT_ID`,
`DOTS_GITHUB_URL` and `DOTS_GITHUB_API_URL`. The last two exist for tests.

## Who is the owner

The owner is the host's own user: a request that reaches the dashboard directly on loopback
(`127.0.0.1`, `localhost`, `[::1]` as the `Host`, from a loopback address) with no
`Tailscale-User-Login` header. The owner sees everything and never signs in.

Every other request is a visitor: one that carries the header, or one whose `Host` is not loopback,
which is what `tailscale serve` forwards. A visitor sees nothing until they sign in.

## Signing in

The page shows a sign-in button. It asks GitHub for a code (the OAuth **device flow**, with no
scope), shows it with a link to github.com, and polls until the visitor approves. The dashboard then
asks GitHub once who the token belongs to, keeps the **login** and drops the token: it is never
stored, written to a log or put in a cookie. The session is in the dashboard's memory only: the
login, an expiry of 8 hours and a random 256-bit id sent as an `HttpOnly`, `SameSite=Strict` cookie
(`Secure` when the request came over https). A restart signs everyone out. "Sign out" in the nav ends
it at once.

Endpoints: `POST /auth/device/start`, `POST /auth/device/poll`, `POST /auth/logout`, `GET /api/session`.
The first three are rate limited per client and check the host, the origin, a JSON content type and a
4 KiB body, like the other writes. The poll honours GitHub's `interval` and `slow_down`; the code
expires with GitHub's `expires_in`.

## What a visitor sees

For each configured repository (the watcher config, the daily list, the path map, the `state/tend`
directories), the dashboard asks GitHub `GET /repos/{owner}/{repo}/collaborators/{login}/permission`
with the dot's token. The repository is shown when the permission is `read`, `triage`, `write`,
`maintain` or `admin`. Any error, any non-2xx answer, an unknown repository or a missing token hides
it: the check fails closed. With sign-in on and no `GH_TOKEN`, a visitor sees no repository and the
page says why.

Filtered to those repositories: artifacts (reviews, replies, seen), a run's log, stream, price,
steps, sessions and replay, `/api/logs`, `/api/usage` (the total, the run count and the unpriced
count are added up again from the visible groups), executors' runs, repos and repo names, memory,
claims, link proposals (only when both items are visible), autonomy notes and held records and their
artifacts, Ask threads, artifact files, and audit rows whose target names a repository.

A record of a hidden repository answers like one that does not exist (404, the same body), appears in
no list, count, total or error message. A run whose repository is not known is hidden.

## What is owner only

A visitor gets 403 here even when signed in, and 401 when not signed in: `/api/save`, `/api/source`,
`/api/files`, `/api/models`, `/api/notifications`, `/api/repos/config`, `/api/autonomy/release`,
`/api/links/accept`, `/api/links/decline`, `/api/links/split`, `/api/ask`, `/api/ask/answer`,
`/api/workers`, `/api/health` and the `/awf/` proxy. The page hides the Activity, Workflows, Settings,
Repos (the `repos.yaml` editor) and Health sections from a visitor, who lands on Cost. Activity is built
on awf's own history, which is the owner's.

## Audit

A visitor's request is recorded with the GitHub login as the caller: a refused write or a refused read
of an owner-only page, a completed sign-in, and each read of a data endpoint (one row per login, path
and target a minute, so a page that polls does not flood the table). See `docs/audit.md`.

## Limits

- Permissions are cached for 60 seconds (5 seconds after a failure), so a revoked right takes up to a
  minute to show.
- A repository whose permission the dot's token cannot read is hidden from every visitor.
- The list of repositories is the dot's own; a run or a record of a repository outside it is hidden
  from visitors.
- No organisation-level rules: the answer is GitHub's permission on the repository, per login.
- Sessions live in memory: a restart signs visitors out.
- The Activity page does not work for a visitor yet; runs show on the Cost page and through
  `/api/logs`.
