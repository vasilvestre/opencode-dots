# 0018. fix-issue works from a fork when it cannot push upstream

- Status: accepted
- Date: 2026-11-25
- Supersedes: none
- Superseded by: none

## Context

`fix-issue` pushed its `fix/<N>-<date>` branch straight to the repository named
in the run. That works on my own repositories. On an upstream where I am not a
maintainer, such as `awf-project/cli`, the token has no write access: the agent
spent up to 25 minutes on a fix and the run failed at the last step, so the
work was lost to a permission error that was knowable before it started.

## Decision

We keep one workflow and decide at the `push` step. The step reads
`permissions.push` of the target repository. With write access it behaves as
before. Without it, it works from my fork `<login>/<repo>`: it reuses the fork
when `gh` reports it as a fork of the target, creates it with `gh repo fork`
when it does not exist, and refuses to push to a same-named repository that is
not a fork of the target. It pushes the branch there and opens the pull request
against the upstream with `--head <login>:<branch>`.

The decision sits in `push`, after the commit exists, so a run that declines or
changes nothing creates no fork.

## Consequences

- An issue on an upstream I cannot write to ends in a pull request instead of a
  failed run.
- A run can now create a repository under my account. The check for a
  same-named non-fork keeps it from writing into an unrelated repository.
- Which repositories get the workflow is still decided by `awf-gh.yaml`; nothing
  here labels or polls an upstream by itself.
- The fork is not kept in sync by the workflow. The step rebases the commit onto
  the fresh upstream base before pushing, so a stale fork does not matter.

## Alternatives

- **Fail early with a clear message.** Cheaper, but the operator still has to
  push by hand from a fork.
- **A second workflow for forks.** The two would differ only in the last step.
- **Check write access in `fetch` and skip the run.** It saves model time but
  does not produce the pull request.
