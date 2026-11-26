# 0026. A claim says a human works on a work item

- Status: accepted
- Date: 2026-11-26
- Supersedes: none
- Superseded by: none

Relates to [0015](0015-dots-take-delegation.md). It does not supersede it:
0015 gives the dot the right to push to a pull request; this record takes that
right away while a human is working on the same work.

## Context

The dot runs on a VPS that is always on, and I work on the same repositories
from a workstation. Nothing tells the dot that I am editing a branch. Today it
is safe only because ownership is read once, in the `owned` step of
`review-pr` and `reply-pr`: my own `fix/*` branch, or a pull request I
delegated with `/dots take` ([0015](0015-dots-take-delegation.md)). Both say
"the dot may push". Neither says "I am pushing right now". If I delegate a pull
request and then open the branch in my editor, the dot can still push a fix
over my working tree.

We want the opposite signal: a human is on this work, keep off. It must work
for a work item with no pull request yet
([0023](0023-tracker-independent-work-item.md)), it must not stop the dot on
unrelated work, and it must clear itself when my laptop closes. Presence from
the tracker (a draft pull request, an assignee) is too coarse and too slow, and
the event bus only knows who is connected, not what they work on.

## Decision

We add the **claim**: an explicit statement that a human works on a work item.
It is a row in the control-plane database
([0024](0024-control-plane-database.md)) with: work item, actor (the identity
from the tailnet), machine, branch, creation time, expiry and last heartbeat.
Every claim carries an actor id, although the first version has one user.

**Lifecycle.** A local agent on the machine creates the claim through the
loopback API on the tailnet, when I check out the work item, and renews it with
a heartbeat. The heartbeat extends the expiry by a short time to live. When the
machine disappears, heartbeats stop and the claim expires by itself. I can also
release it by hand, from the dashboard or the local agent. The server stamps
every time with its own clock; a machine never sends an expiry.

**Effect on the bot.** The dot never writes to a branch that carries an active
claim held by a human. Shell steps check this before they push, as in
[0002](0002-shell-steps-own-side-effects.md): the agent decides nothing here.
With an active claim the step waits, or proposes the change on a separate
branch, and never pushes to the claimed one. This reads the claim as a second
condition next to `owned`. It does not replace it. `/dots take` stays the grant
(may the dot fix this pull request) and the claim is the veto (not now). A
claim wins over a delegation. `/dots stop` is unchanged and revokes the grant
for good; a claim ends by itself. `/dots take` is the operator's own
convention ("work until the pull request or issue is done, for me"), used on
personal projects. It may not exist for every user or project later, so the
claim does not depend on it: it applies whether or not a delegation exists.

**Granularity.** The unit is the work item, not the project. A claim pauses the
bot on that work item and its branches only. The VPS keeps working on every
other work item of the same repository.

**Two human claims** on one work item raise a warning to both holders, in the
dashboard and through the bus. They are never blocked: two people may
deliberately work on one item.

**Fallback.** With no local agent connected, the dot reads soft signals visible
in the tracker: a draft pull request, a label, an assignee. They are weaker
than a claim. They only decide when no active claim exists, and the first
version uses them to wait, never to refuse.

**Transport.** The bus (Mercure) tells machines about events and claims: it
publishes claim created, renewed and released. It is not the store. Its
active-subscriptions API reports presence, but it is off by default and says
only that a machine is listening, not what it works on. We may show it as a hint
in the dashboard. The claim in the database is the truth, and the bus can be
down without a claim being lost.

**MVP scope.** One user, one claim per work item and machine, soft-signal
fallback in wait mode, warning on a second claim. Rights per user and the
choice of a separate branch name come later.

## Consequences

- The bot cannot overwrite what I am editing, and it keeps working elsewhere.
- A new moving part: a local agent that must run to hold a claim. Without it,
  only the weak tracker signals protect me.
- A claim that lingers blocks the bot. The time to live bounds this: a claim
  left behind by a crashed machine expires; one left by a forgotten, still
  running agent stays until I release it. The dashboard lists active claims
  with their age.
- Heartbeat lost: the claim expires and the bot resumes. A push already made
  stays, as with `/dots stop`. A short network cut that outlasts the time to
  live can let the bot push once. A time to live of several heartbeats absorbs
  most cuts, and a pushed commit is a normal commit to merge.
- Clock skew: only the server's clock counts, so a wrong laptop clock changes
  nothing.
- A claim touches the same rows as work items, so a lost database loses claims.
  They are short lived, so this costs little.
- Check and push are not atomic. A claim created while a push is under way loses
  the race. The step re-reads the claim just before the push to keep the window
  small.

## Alternatives

- **A lock file in git.** Needs a commit and a push to claim, leaves the lock
  behind when a machine dies, and shows the claim only after it is pushed.
- **GitHub assignee only.** Visible, but it exists for issues, not for work with
  no tracker item, it has no expiry, and it does not say which machine or
  branch. We keep it as a fallback signal only.
- **Presence from Mercure subscriptions only.** It is off by default, says a
  machine is connected, not that I work on this item, and leaves no record. It
  is a good transport and a poor truth.
- **A hard exclusive lease per project.** Simple, but it stops the cloud on every
  other work item of a repository whenever I touch one branch, which defeats the
  reason for the VPS.
