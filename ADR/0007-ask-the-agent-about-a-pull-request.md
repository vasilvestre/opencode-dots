# 0007. Ask the agent about a pull request from the dashboard

- Status: accepted
- Date: 2026-11-23
- Supersedes: none
- Superseded by: none

## Context

`review-pr` and `reply-pr` decide alone. When one of them declines ("I do not
want to choose alone", `stuck.md`), the only way forward today is to edit a
workflow or wait for the next trigger. The operator wants to write a sentence to
the agent and read the answer, the way a chat works, and to keep the exchange
around: the question often carries context the agent cannot see, like feedback
received somewhere else.

The page can already start a run (`POST /awf/.../run`) and stream one
(`/api/stream`). Two designs were on the table:

1. A workflow, `ask-pr`, whose agent step reads a transcript of the
   conversation and writes one answer. The page owns the transcript.
2. Driving opencode's own HTTP server (`127.0.0.1:4096`) from the page, the way
   the opencode UI does.

Option 2 was rejected. The agent there holds every tool: it could commit, push
or comment, and the project rule is that shell steps own remote writes, never
the agent. Its API is also unversioned, so the page would break with an
opencode upgrade, and no transcript would land in `state/`, which is the only
thing that survives a restart.

## Decision

Add `workflows/ask-pr.yaml`: inputs `repo`, `pr`, `question`; steps `fetch`
(`gh pr view`, `gh pr diff`, comments and reviews merged into
`pr.thread.json`), `ask` (one agent step) and `done`.

The conversation is a file per pull request:
`state/asks/<owner>/<repo>/pr<N>/{thread.json,thread.md,answer.md}`.

- The page writes `thread.json` and `thread.md` before starting the run.
  `thread.md` is what the agent reads, so every turn remembers the earlier
  ones. awf keeps no per-run session across runs (see ADR 0006), so the file is
  the memory.
- The agent writes `answer.md` only, then the page appends it to the thread as
  one run. A run that fails leaves the thread unchanged, minus the question,
  which is rolled back on a failed start.
- The starting context comes from disk, never from re-fetching: the prompt
  points the agent at `state/replies/pr<N>/` (the answer an earlier run posted,
  `diff.patch`, and the read-only checkout under `work/`) before it falls back
  to the fresh GitHub data.

The agent must not act. The prompt forbids editing files other than
`answer.md`, forbids `git commit`, `git push` and `gh pr comment`, and asks for
"what I would need to know" instead of a guess. No clone is made, so there is no
branch to push even if the prompt is ignored. This is the same trust model as
`review-pr`: the shell steps write, the agent only produces text.

Writes to `state/asks/` are covered by ADR 0004's rules: loopback host,
JSON content type, `POST` only, 16 KiB cap, `repo` and `pr` validated against
`repoNameRE` and `prNumberRE`.

The answer may name the next step. The agent ends `answer.md` with one or two
lines of the form `SUGGEST: reply-pr repo=owner/repo pr=42`; the page removes
them from the text it shows, keeps them on the message in `thread.json`, and
renders one button per line. A button opens the normal run dialog with the
repository and pull request filled in, so the operator still reads the inputs
and presses Run now. The agent suggests, it never starts anything: awf can chain
workflows (`call_workflow`), but letting an answer about a chat message start a
workflow that pushes code is what ADR 0002 rules out.

A name outside `review-pr`, `reply-pr`, `fix-issue` and `tend-repo`, or a line
the page cannot parse, is dropped together with the line, so a malformed
suggestion never reaches the reader as raw text.

## Consequences

- Each question costs one agent turn, at the price shown next to the answer
  (read from opencode's database, ADR 0005).
- The thread survives a page reload and an awf restart, because it lives in
  `state/`, like every other artifact.
- The repository comes from the artifact: `review-pr` now writes a `repo` file
  next to `meta.json`, and `reply-pr` reads the base repository from `pr.json`'s
  `url`. Artifacts older than that get no Ask button rather than a wrong one.
- Nothing answers automatically: a PR keeps whatever verdict the last workflow
  wrote until someone runs it again. Making the answer feed back into a run is
  a separate decision.
