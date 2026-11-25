# 0012. The fix rule judges decisions, not line counts

- Status: accepted
- Date: 2026-11-23
- Supersedes: none
- Superseded by: none

## Context

`fix-issue` tells the agent to stop when a fix "needs more than about forty
lines, or a decision I would have to make". The two conditions are presented as
equal. Only the second one earns its place.

On 2026-11-23 the agent read issue `vasilvestre/awf-gh#11`, which asks for a
`Makefile`, a `.goreleaser.yaml`, two GitHub workflows and a `-version` flag. The
issue already named the deliverables, the acceptance checks, the Go version
(`1.24`, do not bump) and the exact action tags to use. Nothing was left open.
The agent still wrote no code: `state/fix/vasilvestre/awf-gh/issue-11/push.log`
records `changed=0`, and its answer is a draft ADR instead of a patch.

The line count refused the work because the work is configuration. A hundred
lines of YAML in a `Makefile`, a goreleaser file and two workflow files decides
less than ten lines of Go in an authentication check. Counting lines measures
the size of the diff, and the diff size is not the risk.

## Decision

We replace the single threshold with two rules that name what actually differs.

The agent implements what the issue settles. When the issue, an accepted ADR or
the maintainer's comment on the issue names the files, the versions and the
acceptance checks, the agent implements it, however many lines that takes.
Configuration volume is not a reason to stop.

The agent writes no code for a decision nobody asked it to make. When the issue
leaves a choice open, such as which tool, which dependency, which public
interface, what data may be deleted, or what may be written to a remote, it
answers with a draft ADR and no patch. The maintainer files the record.

## Consequences

A detailed issue now becomes a pull request, which is what the issue was for.

The remaining guard is judgement, not arithmetic. The agent decides whether a
choice is open, and it can be wrong in both directions: it can implement a
change the maintainer wanted to arbitrate, and it can defer one that was
settled. The cost of over-implementing is one review round, because
`review-pr` gates the merge and ADR 0002 keeps every remote write in a shell
step. The cost of deferring is a silent no-op, which is the failure this record
removes.

The rules get longer to read, which is the price of replacing a number with a
test.

## Alternatives

Keep the threshold and raise it to a hundred lines. It would still refuse the
next decided issue, and it would allow a hundred lines of a change nobody
reviewed the design of.

Judge risk by touched paths, allowing the config directories and refusing the
rest. Paths say where a change lands, not what it decides; a workflow file can
write to a remote branch, and a test file decides nothing.

Let the agent file its own ADR in the repository instead of replying with it.
That adds a write path outside the checkout that no step reviews, and the
maintainer loses the moment where the decision gets confirmed.
