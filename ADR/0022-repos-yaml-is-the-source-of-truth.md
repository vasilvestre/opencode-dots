# 0022. repos.yaml is the source of truth for the repository list

- Status: superseded by 0024
- Date: 2026-11-25
- Supersedes: none
- Superseded by: 0024

## Context

What the dot does on one repository was spread over four local files, each read
by a different tool and none naming the others: `awf-gh.yaml` (one line per
repository and workflow, 41 of them, with the same `ignore_authors` repeated),
`tend.list` (`repo auto_merge`), `repos.map` (`repo=checkout`, read by an `awk`
in five workflows) and `sync.map` (`repo=checkout`, read by `bin/sync`). To know
what happens on a repository, or to turn a repository on or off, you opened
several files, and a new per-repository setting such as the agent CLI had
nowhere to live: it would have been a fifth file or a repeated input. Each
file carried its own paths, so the links between them were conventions.

The readers are fixed. `awf-gh` is a separate project with its own format,
`bin/tend` and `bin/sync` are shell scripts, and the workflows read `repos.map`
with `awk`; the image has `jq`, not a YAML parser.

## Decision

We make `repos.yaml` the one file to edit, and generate the four others from it.
A repository is one block of feature keys (`review`, `reply`, `fix_issue`,
`fix_ci`, `tend`, `checkout`, `sync`, `provider`). `defaults` give values to
every repository that enables a feature and never switch one on, `profiles`
bundle keys a repository takes with `use`, and `false` switches an inherited key
off. A plain default such as `provider` applies to every repository.

`bin/repos generate` (also `make repos`, and the dashboard when it saves
`repos.yaml`) writes `awf-gh.yaml`, `tend.list`, `repos.map` and `sync.map`,
each with a "generated" header, only when their content changed. The consumers
keep reading the formats they know. `awf_url` and `state_path` stay out of
`repos.yaml`: they depend on local or docker mode, so `bin/configure` passes
them and the generator otherwise keeps what the generated file already has.

Every key is a registered Go feature that checks its own value and says what it
adds to the outputs. A new key is a new file, not a change to the loader; an
unknown key is an error that names the known ones. `bin/repos import` builds the
first `repos.yaml` from the four existing files, and its test regenerates them
and compares the result rule for rule.

## Consequences

- One place says what the dot does on a repository, and a repeated block becomes
  `true` or a default. The imported file of the current setup is 115 lines
  against 129 for `awf-gh.yaml` alone.
- A typo, an unknown event or an `issue-labeled` rule without a label is refused
  when `repos.yaml` is saved, before it can stop a polling pass.
- The agent CLI per repository (`provider: claude`) has a home: it becomes the
  `provider` input of the workflows that declare one, and is not sent to the
  others.
- The four generated files can no longer be hand edited: the dashboard refuses
  the save while `repos.yaml` exists. Their formats are unchanged, so a run that
  started before the change is unaffected.
- Comments and YAML anchors of the old files do not survive an import.
- A feature is code, not configuration: adding a capability needs a Go change
  and a rebuild of `bin/repos`.

## Alternatives

- **Read `repos.yaml` directly from the workflows and `bin/tend`.** Cleaner end
  state, but the shell has no YAML parser, and `awf-gh` would have to change in
  its own repository. Generation keeps every consumer as it is and can be
  replaced by direct reads one tool at a time.
- **One more file, `providers.map`.** It adds the multiplication this record
  removes.
- **Put the provider in `repos.map`.** Its format is read by an `awk` in five
  workflows and by the dashboard, and a repository without a local checkout has
  no line in it.
- **Plugins as external executables.** More open, but a protocol, a lookup path
  and a trust question for a single user. The registry is the same extension
  point and can be given a subprocess adapter later.
