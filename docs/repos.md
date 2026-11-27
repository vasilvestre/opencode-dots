# The list of repositories



One file says which repositories the dot works on and what it does on each:
`repos.yaml`. The names are private and this repository is public, so it is
gitignored and only `repos.example.yaml` is versioned:

```bash
go build -o bin/repos ./cmd/repos
bin/repos import        # existing install: builds repos.yaml from awf-gh.yaml, tend.list and repos.map
cp repos.example.yaml repos.yaml   # new install only, never over an existing awf-gh.yaml
$EDITOR repos.yaml      # one block per repository
make repos              # writes awf-gh.yaml, tend.list, repos.map and sync.map
make configure          # fits awf_url and state_path to local or docker mode
```

A repository block holds feature keys: `review`, `reply`, `fix_issue`, `fix_ci`
(the watcher rules), `tend`, `checkout`, `sync` and `provider`. `defaults`
supply values to every repository that enables a feature (they never switch one
on), and a `profiles` entry bundles keys a repository takes with `use`.
`bin/repos features` lists the keys with what each does.

`awf-gh.yaml`, `tend.list`, `repos.map` and `sync.map` are generated: do not edit
them. The dashboard validates `repos.yaml` when it saves it, refuses a file that
would not generate, and rewrites the four files. `make configure` regenerates
`awf-gh.yaml` with the `awf_url` and `state_path` that fit local or docker mode.

Coming from the four hand-written files, `bin/repos import` builds the first
`repos.yaml` from them, and its test shows regenerating gives the same rules.
See [ADR 0022](../ADR/0022-repos-yaml-is-the-source-of-truth.md).
