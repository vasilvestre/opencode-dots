# Vulnerable dependencies

The first work source wired end to end (ADR 0039): advisories from `composer audit`
become tracked, classified work items and an internal note. It reads, records and
writes a note. It never upgrades anything and never runs a line of the project's code.

## Flow

```
repos.yaml  advisories: true
   |  make repos
   v
advisory.list              one owner/repo per line, generated, gitignored
   |
   v
bin/advisories             per repository with a local checkout and a composer.lock:
   |  1. copy composer.lock and composer.json to a temporary directory
   |  2. composer --no-plugins --no-scripts audit --locked --format=json   (on the copy)
   |  3. bin/repos db ingest -source advisory -repo OWNER/REPO -lock composer.lock -json
   |  4. for each NEW or CHANGED advisory: a note and a class file under $STATE_DIR/notes/
   v
dots.db                    one work item per advisory, reference advisory:<id>@<owner>/<repo>
```

Turn it on per repository, and only `true` is accepted:

```yaml
repos:
  acme/shop:
    advisories: true
    checkout: /home/me/work/acme-shop
```

It is off by default. A repository is audited only if its clone is found: the `repos.map`
line (`checkout:`), else `$WORK_ROOT/owner/repo` (`WORK_ROOT` defaults to `$STATE_DIR/work`).
A repository with no clone or no `composer.lock` is skipped with a line, not an error.

## The store operation

`bin/repos db ingest -source advisory -repo OWNER/REPO [-lock composer.lock] [-json]` reads
`composer audit --format=json` on stdin and, for each envelope of `internal/sources/advisory`:

- creates the work item for the reference `advisory:<id>@<owner>/<repo>`, attached to the project, or
  finds it. An item still stored as `advisory:<id>` for the same repository is the same item: its
  reference is rewritten, an `advisory.ref_migrated` event records it, and the line says `seen`
  (or `changed`) as usual, with no new note;
- a repeat with the same payload adds nothing (`seen`);
- a changed payload (a new severity, a different fixed version) replaces the stored one and
  appends an event holding the previous and the new payload (`changed`);
- prints one line per advisory: `new|seen|changed`, the reference, `level=N`, `severity=...`, the
  packages. `-json` prints the full result instead, which `bin/advisories` reads.

The repository must be a project of the database: run `bin/repos db import` once
(`bin/advisories` does it itself when `repos.yaml` is present and the project is unknown).
A reference belongs to one work item (ADR 0023), and an advisory work item is per repository: the
same advisory in two repositories is two references, two work items and two notes, and a repeat
in one repository is the same item. A `refused` line is now only possible when two projects share
one repository name; the other advisories of the report are still recorded and `bin/advisories`
writes no note for it and does not fail. The old global form `advisory:<id>` stayed with the first
repository that reported it; on the next ingest of that repository it is rewritten (see above), and
every other repository gets its own item. References come from the structured GHSA or CVE
field of the report, never from a title or any other text.

Tables: migration 5 adds `work_item.payload` (the latest payload) and `work_item_event`
(append-only trail: `advisory.created`, `advisory.changed`).

## Classification

Pure rules, no agent, in `workitem.ClassifyAdvisory`. The levels are those of
`docs/classification.md`. The installed version comes from `composer.lock`.

| Level | When |
| --- | --- |
| 1 | every affected package has a fixed version in the installed major version: a plain upgrade |
| 2 | only a new major version fixes it, or the installed version is unknown or not a release |
| 3 | a package has no fixed version, or the advisory is `critical` on a package whose name starts a word of `workflows/data/classify-floor.txt` |

The worst package decides. The floor reads package names only, never the advisory text, and only
raises. Without the floor file nothing is classified, as in `_classify-item`. The result goes to
`$STATE_DIR/notes/advisory-<owner>__<repo>__<id>.class.json`, in the shape of `classify/<key>.json`
(`repo`, `key`, `classified_at`, `level`, `justification`, `floor`) plus `rules: advisory`.

## The note

For every new or changed advisory `bin/advisories` writes a short Markdown summary (package, installed
version, affected range, fixed versions, severity, link, work item) and passes it to
`bin/publish-gate comment` with `AUTONOMY=observe`, `ITEM_CLASS` set to the level and
`GATE_REF=advisory:<id>@<owner>/<repo>`. The ceiling is fixed to `observe` on purpose: this flow only ever leaves a
note under `$STATE_DIR/notes/` (the gate copies the artifact next to it), whatever the repository's
own autonomy. Nothing is published.

Advisory text is data. Titles, links and package names are stripped of control characters and
capped before they are stored or printed (title 200, link 300 runes); a link survives only as an
http or https URL without spaces or markup; the title appears once, inside a code span, labelled as
third-party text.

## What it never does

- No upgrade: no `composer update`, no branch, no pull request. Opening an upgrade pull request needs
  a decision on running `composer update` in a sandbox with network, which is the owner's.
- No project code: composer gets a temporary directory with copies of `composer.lock` and
  `composer.json` only (no `vendor/`, so no plugin can load), with `--no-plugins --no-scripts`.
  The clone is never written to. `GH_TOKEN`, `GITHUB_TOKEN` and `COMPOSER_AUTH` are removed from
  composer's environment, and credentials in a printed error line are masked.
- No post, comment, label or merge. No token is printed.
- It does not need composer to be installed: without it, it prints `composer not found` and exits 0.

`composer audit` queries the advisory API of the configured repositories, so it uses the network
itself; that is composer's own call, not the project's code. `--no-dev` is left off on purpose
(composer audits dev packages by default; `--no-dev=false` is not a valid flag).

## Environment

`STATE_DIR`, `ADVISORY_LIST` (default `advisory.list`), `REPOS_MAP`, `WORK_ROOT`, `REPOS_BIN`
(default `bin/repos`), `COMPOSER`, `ADVISORY_NOTE_PAUSE` (seconds between notes, default 1: the gate
names a record by the second and the reference). Exit 0 when nothing is wrong or there is nothing to
do, 1 when a repository could not be audited or recorded; the others are still done.

## Running it daily

No unit file ships with the repository. To run it from systemd, put these two files in
`~/.config/systemd/user/`, then `systemctl --user daemon-reload` and
`systemctl --user enable --now dots-advisories.timer`.

`dots-advisories.service`:

```ini
[Unit]
Description=Audit the composer dependencies of the listed repositories (docs/advisories.md)

[Service]
Type=oneshot
WorkingDirectory=%h/projects/perso/opencode-dots
EnvironmentFile=-%h/projects/perso/opencode-dots/.env
ExecStart=%h/projects/perso/opencode-dots/bin/advisories
```

`dots-advisories.timer`:

```ini
[Unit]
Description=Daily advisory audit

[Timer]
OnCalendar=daily
RandomizedDelaySec=1h
Persistent=true

[Install]
WantedBy=timers.target
```

Adjust the path to where the repository lives, and set `STATE_DIR` (and `WORK_ROOT`) in the unit if
they are not the defaults. The `.env` line is optional: `bin/advisories` needs no token.

## Tests

`bin/test-advisories` (fake `composer` on `PATH`, fixtures in `test/advisories/`, a temporary
database): first ingest, repeat, changed severity, no `composer.lock`, composer missing, composer
failing, hostile text. `go test ./internal/workitem ./internal/store ./internal/repos` cover the
store operation, the rules and the feature.

## Not done

Upgrade pull requests, a reachability check, resolving a work item once the advisory leaves the
report, Sentry and Jira ingestion, and `repos db import` of the dashboard view of these items.
