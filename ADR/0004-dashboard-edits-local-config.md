# 0004. The dashboard edits local config files

- Status: superseded by 0017
- Date: 2026-11-23
- Supersedes: none
- Superseded by: 0017

## Context

The dashboard reads `state/`, proxies the awf API, and writes nothing except
`state/logs/`. awf exposes no write API for workflows: it lists, reads,
validates and runs them, but never saves one. The only way to change a workflow
or the watcher config is to edit a YAML or text file on disk.

Editing those files by hand is the remaining step that keeps the page
read-only. A save needs three new interfaces on a loopback HTTP server, so the
change touches security and needs a record.

## Decision

We let the dashboard edit four kinds of file, and only those: `workflows/*.yaml`,
`awf-gh.yaml`, `tend.list` and `repos.map`. A `PUT /api/save` handler requires
`Content-Type: application/json`, requires the `Host` header and any `Origin`
header to name a loopback address, refuses any path outside the allowlist, caps
the body at 1 MiB, parses YAML before writing, and keeps the previous content as
`<file>.<timestamp>.bak`, pruned to the last twenty per file. The page writes
the file and stops: no git commit, no git add. Validation and run keep going
through the existing `/awf/` proxy, so the page gains no new privilege over awf.

The frontend becomes a set of ES modules under `ui/`, still without a build
step, served by the same binary.

## Consequences

Saving a workflow is one click. Git stays the undo history for tracked files;
the backups cover the private files git ignores. The page must keep its security
checks in one place, and a single missing check gives a local page the ability
to rewrite the watcher. There is no authentication: the loopback bind and the
Origin, Host and content-type checks are the whole boundary, so the dashboard
must never listen on a public interface. Workflows that come from a pack or from
`env` scope have no file here and stay read-only.

## Alternatives

- Visual graph builder: awf workflows are mostly multi-line shell commands; a
  node editor would lose the comments, anchors and scripts that make them
  readable.
- Commit from the page: the dashboard would become an actor on the repository,
  against the rule that shell steps own side effects (ADR 0002).
- Export only: a generated file to paste elsewhere does not remove the manual
  step the change exists to remove.
