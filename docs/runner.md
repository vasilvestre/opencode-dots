# Runner for untrusted steps

Two scripts for steps that touch code written by others, the pattern named in
[ADR 0032](../ADR/0032-trust-levels-per-repository.md): a model's output is
checked and applied by a shell step that holds the token, and tests of untrusted
code run in a throwaway container. Neither is wired into a workflow yet.

## `bin/sandbox-run IMAGE WORKDIR -- CMD...`

Runs `CMD` in a container that is removed afterwards, with:

| Guarantee | Flag |
| --- | --- |
| no network | `--network none` |
| read-only root filesystem | `--read-only` |
| no Linux capabilities, no privilege gain | `--cap-drop ALL`, `--security-opt no-new-privileges` |
| bounded processes, memory, CPU | `--pids-limit 256`, `--memory 1g`, `--cpus 2` |
| scratch space only in memory | `--tmpfs /tmp` (`noexec`, 256m) |
| runs as the caller's uid, not root | `--user $(id -u):$(id -g)` |
| wall-clock limit | `timeout 600`, exit 124, container removed |
| only a named image family | `IMAGE` must start with `DOTS_SANDBOX_IMAGE_PREFIX` (default `dots-`) |

`WORKDIR` is mounted at `/work`, **read-only** by default. Set
`DOTS_SANDBOX_WRITABLE=1` when the command must write there (build output). Limits
are tunable with `DOTS_SANDBOX_TIMEOUT`, `_MEMORY`, `_PIDS`, `_CPUS`,
`_TMPFS_SIZE`. `DOTS_SANDBOX_ENGINE=podman` works for a rootless podman. Nothing
needs `/dev/kvm`; for stronger isolation point the engine at a `gVisor` runtime
(`--runtime runsc` is not set here, configure it as the engine default).
`--print` shows the command without running it.

Exit status is the command's, 124 on timeout, 2 on a usage error or a refused image.

### What it does not guarantee

- It is a container, not a microVM: a kernel escape defeats it. Rootless mode and
  `gVisor` reduce that risk, they do not remove it.
- The command reads everything in `WORKDIR`. Do not mount a directory that holds
  secrets, other repositories, or the `.git` of a clone with credentials. Give it a
  fresh checkout or an export of the tree.
- The image is trusted by name only. The prefix stops a typo or a model-chosen
  image, not a tampered `dots-*` image.
- With a writable `WORKDIR`, whatever the command writes is untrusted output: do not
  execute it on the host, and run it through `patch-guard` if it becomes a diff.
- No network also means no dependency install: bake dependencies into the image or
  vendor them first.
- A timeout limits time, not output size. Cap the log the step keeps.

## `bin/patch-guard`

Reads a unified diff on stdin. Exit 0 passes; exit 1 refuses with the reason on
stderr; exit 2 is a usage error.

```
bin/patch-guard [--allow-path GLOB]... [--deny-path GLOB]... [--allow-locks] [--max-lines N]
```

- `--allow-path`: when given, every touched path (including rename and copy
  targets) must match one. `--deny-path` wins over it.
- Globs are shell patterns where `*` crosses `/`; a glob without `/` matches the
  file name at any depth.
- Always refused: absolute paths and `..`; `.git`, `.gitmodules`, `.gitattributes`;
  secrets (`.env*`, `*.pem`, `*.key`, ssh keys, `*secret*`, `*credentials*`, `.netrc`,
  `.npmrc`); CI config (`.github/`, `.gitlab-ci.yml`, `.circleci/`, `Jenkinsfile`
  and similar); symbolic links; binary changes.
- Lock files (`package-lock.json`, `go.sum`, `Cargo.lock`, ...) are refused unless
  `--allow-locks`.
- `--max-lines N` bounds added plus removed lines.

It checks the diff text, not the repository: it cannot tell what a change does.

## How a workflow step calls it

The agent writes a diff to a file; shell steps check, test, then apply. The agent
never commits or pushes (see `AGENTS.md`).

```bash
bin/patch-guard --allow-path 'src/*' --max-lines 400 < "$STATE_DIR/proposal.diff"
tmp=$(mktemp -d)
git worktree add --detach "$tmp" HEAD
git -C "$tmp" apply --index "$STATE_DIR/proposal.diff"
bin/sandbox-run dots-test "$tmp" -- make test
git apply --index "$STATE_DIR/proposal.diff"   # only after the test passed
```

A refused diff or a failing test ends the step with a non-zero exit, which awf
routes to its failure state.

## Tests

`bin/test-sandbox` covers `patch-guard` and the `sandbox-run` flags without docker.
Build the minimal test image from `deploy/sandbox/` and point the test at it:

```bash
docker build -t dots-sandbox-test deploy/sandbox
DOTS_TEST_IMAGE=dots-sandbox-test bin/test-sandbox
```

Without docker or an image starting with `dots-` the real-container part prints a
skip message.

### Verified in a real container

Run on a rootful docker 29 engine, cgroup v2, runc, no `/dev/kvm` needed or used.
Each line is a check in `bin/test-sandbox` that passes:

- no network: only `lo` exists, an outbound `wget` and a raw `nc` connect fail;
- read-only root: writes to `/`, `/etc`, `/usr` fail;
- capabilities: `CapEff` and `CapBnd` are zero, `chown` is refused;
- `NoNewPrivs` is 1; the process runs as the caller's uid;
- pids limit: 40 background sleepers fail to fork at 8 and succeed at 256;
- memory limit: a 200 MB allocation under `DOTS_SANDBOX_MEMORY=32m` is killed (137);
- `/tmp` is writable, `noexec`, and capped by `DOTS_SANDBOX_TMPFS_SIZE`;
- `WORKDIR` is read-only by default (and the host directory stays untouched),
  read-write with `DOTS_SANDBOX_WRITABLE=1`, and then it and `/tmp` are the only
  writable places;
- timeout: a `sleep 30` under `DOTS_SANDBOX_TIMEOUT=2` exits 124 and leaves no
  container behind; the command's own exit status passes through;
- an image outside the prefix is refused (exit 2), even when it exists locally.

### Not verified

- Rootless docker and podman: the machine used was rootful. `DOTS_SANDBOX_ENGINE=podman`
  is untested. Under rootless, `--user` with the caller's uid and bind-mount
  ownership may behave differently.
- `gVisor` or any other runtime: not installed, not tried.
- `--cpus` is set but no test measures CPU throttling.
- A kernel or runtime escape: out of scope for a container.
- Nothing is wired into a workflow, so the call sequence in the section above has not
  run end to end.
