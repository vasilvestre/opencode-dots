# 0030. The GitHub watcher is opt-in

- Status: proposed
- Date: 2026-11-26
- Supersedes: none
- Superseded by: none

## Context

[0022](0022-repos-yaml-is-the-source-of-truth.md) made `repos.yaml` the one file
to edit, and `bin/repos generate` refused a file that holds no watch rule
(`review`, `reply`, `fix_issue` or `fix_ci`), because `awf-gh` refuses an empty
list. A file with only `tend: true` could not be generated.

That rule assumed every installation polls GitHub. The first always-on server
([0027](0027-eu-vps-native-local-mode.md)) only runs the daily `tend` pass, and
the owner had to edit the generated `tend.list` by hand, which the project
forbids. The assumption is also wrong for later setups: a tracker other than
GitHub ([0023](0023-tracker-independent-work-item.md)) would have no use for
`awf-gh`, and the generator would still demand a rule for it.

## Decision

We make the watcher opt-in. `awf-gh.yaml` is generated only when at least one
repository enables a watch rule. A `repos.yaml` without any is valid, and the
other generated files (`tend.list`, `repos.map`, `sync.map`) are written as
usual. The refusal of an empty list is removed, since the empty list is never
written.

`bin/repos generate` says so when it leaves the watcher file out and an
`awf-gh.yaml` from an earlier run is still on disk: that file is not touched and
still drives `awf-gh`, so it must be removed by hand to stop the watcher.

## Consequences

- A server that only tends has a valid `repos.yaml` and no hand-edited
  generated file.
- A setup without GitHub polling is not blocked by the generator.
- Removing every watch rule no longer produces an error that would have flagged
  a mistake: the watcher file is just not generated. The notice on stderr is the
  only signal, and only when an old file is present.
- A stale `awf-gh.yaml` keeps running after the last rule is removed, until it is
  deleted. The generator does not delete it, because it never removes a file it
  did not create in this run.
- Nothing changes for a file that has a watch rule.

## Alternatives

- **A setting that switches the watcher off explicitly.** It keeps the refusal as
  the default and states the intent, at the cost of a second way to say "no
  watcher", since having no rule already says it.
- **A command-line flag such as `--only tend.list`.** It leaves the file alone and
  makes every caller remember the flag.
- **Keep the refusal and add an inert watch rule on the server.** No code change,
  and a rule that exists only to satisfy the generator.
- **Delete a stale `awf-gh.yaml` when no rule remains.** It stops the watcher, and
  it makes the generator remove a file that a person may have placed on purpose.
