# 0006. Memory of one pull request across runs

- Status: accepted
- Date: 2026-11-23
- Supersedes: none
- Superseded by: none

## Context

`reply-pr` fires once per review comment, `fix-issue` once per label, and each
run starts from a fresh worktree with a fresh agent session. So the agent on a
pull request's third round does not remember that it already answered the same
point in round one, and it re-reads the whole thread to find out.

awf tracks a session and resumes it with `opencode -s <id>`, but only inside
one run: the provider takes no session ID as input, `awf-gh` keeps a dedup key
set with no values, and the `awf` container does not mount opencode's session
store. Resuming a live session across runs would also bring a stale checkout
and a context that grows every round, which the measurements in
[`docs/memory.md`](../docs/memory.md) say costs quality as well as tokens.

## Decision

We give each pull request and each issue a short worklog under its state
directory, and an indexed record in tower memory. The agent reads `worklog.md`
first when it exists and rewrites it at the end (at most 25 lines: what was
asked, what was decided or changed, the pushed commit, what is still open). It
then stores one `session_summary` record in tower memory, with the pull request
or issue number as provenance, when the tower tools are available. `fetch`
recreates the directory it writes to, so it copies `worklog.md` aside and puts
it back.

The worklog is the part that works in every mode: it is a file under
`STATE_DIR`. Tower memory only reaches the agent when tower is declared as an
MCP server for the opencode instance that runs the step. In `local` mode the
host opencode server already declares it, and the store lives in the server's
working directory, so it survives the runs. In `docker` mode the image ships no
tower: the Linux release tarballs are empty (awf-project/tower#99) and
`WITH_TOWER=0` stays until they ship. The prompts therefore treat the tower
tools as optional and skip the search and the record when they are missing.

## Consequences

A second run on the same pull request starts with the earlier decisions in
about 300 tokens instead of re-reading the thread, and the answer stays
consistent with what was already pushed. Searching tower finds the notes from a
different workflow or from the interactive agent.

The worklog is only as good as the agent's rewrite: a wrong line misleads the
next run, so the prompt orders a rewrite, not an append, and the cap is 25
lines. Per-repository `lessons.md` and `memory.md` keep their role; the worklog
is about one thread, not about the repository. Tower memory is a bonus that
disappears silently in `docker` mode, and its store is per workspace, so moving
the working directory loses the records. A state directory that is reused by
hand for a different pull request keeps the old worklog: `fetch` writes into
`replies/pr<N>/` and `fix/<repo>/issue-<N>/`, keyed by number and repository.

## Alternatives

- **Resume the opencode session across runs.** It needs an awf change to accept
  a session ID, `awf-gh` to store it per pull request, and a mount for the
  session database. It also carries a stale checkout and a growing context into
  every round.
- **Only tower, no worklog.** Tower stores and searches fine, but the agent must
  still read the thread on a cold start, and a record it wrote is not guaranteed
  to be found by the query it writes. A plain file at a known path costs one
  `read`.
- **Let the review threads be the memory.** They are the record, but they do not
  say what the agent decided and rejected, and every run pays to re-read them.
