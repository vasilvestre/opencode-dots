# Two dots on one repository

ADR 0038, superseded by [ADR 0045](../ADR/0045-dots-do-not-talk-to-each-other.md) (dots do not talk to each other): the `dot` key stays as the one way to say, by configuration, which dot acts on a repository. Two dots (a personal one and a team's, or one per
teammate) can end up watching the same repository: a double review, two pushes. The `dot` key
settles it by configuration, with no network between the servers.

## Set it up

On each server, give the server its own id and name the owner of the shared repository:

```yaml
settings:
  dot_id: workstation        # this server; lowercase letters, digits, - and _, 1 to 40

repos:
  your-org/shared:
    dot: workstation         # this dot acts here
    review: true
    tend: true
```

On the other server the same repository carries `dot: workstation` and its own
`settings.dot_id: colleague`. That server then treats the repository as observe-only. `dot` also
works in `defaults` and in a profile.

## What it does

- `dot` requires `settings.dot_id`; without it `bin/repos` refuses the file and says so.
- A repository whose `dot` differs from this server's `dot_id` gets nothing generated: no rule in
  `awf-gh.yaml` (or an executor watcher file), no line in `tend.list`, `repos.map`, `sync.map` or
  `breaker.list`.
- Each generated file then carries a comment such as
  `# skipped: 3 repositories owned by another dot`. It is a count: names stay out of generated
  files.
- A repository with no `dot`, or with this server's own, is generated exactly as before. A file
  with no `dot` key generates byte-identical output.
- `bin/repos check` compares against what would be generated, so skipped repositories are
  expected and never reported as missing.
- `bin/repos features` lists the key.

## What it does not do

- No exchange between servers: no shared claims, no shared lessons, no shared memory. Claims
  (`docs/claims.md`) and lessons stay per server.
- It does not stop a server whose `repos.yaml` lacks the key: a forgotten key leaves two dots
  acting. A health warning is left for later.
- The database path of `bin/repos db` does not carry `dot_id` yet.

## Move a repository between two dots

Change the key on both sides, then run `make repos` on each: set `dot: <new owner>` in the old
owner's `repos.yaml` and in the new owner's. The old owner stops generating it, the new one starts.
Until both have run, either none or both act; run the new owner second if you prefer a short
overlap to a gap.
