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

## In a repository: the `sandbox` key

A repository that opts in has every push checked **by the publish gate**, the one place every outward write
passes (ADR 0002). The four workflows that push (`review-pr`, `fix-issue`, `fix-ci`, `reply-pr`) hand the gate the
patch and the checkout; the gate runs `bin/patch-guard` on the patch and, with a test configured, `bin/sandbox-test`
on the checkout. A refusal is exit 5, the push does not happen, and the reason is a note the dashboard shows.

```yaml
repos:
  acme/client-app:
    trust: client
    sandbox:
      max_lines: 300                      # added plus removed lines, default 400, up to 5000
      allow_paths: ["src/*", "tests/*"]   # optional: every touched path must match one
      allow_locks: false                  # lock files are refused unless true
      test: {image: dots-test, command: make test}   # optional, in a throwaway container
```

- An empty block (`sandbox: {}`) takes the defaults and checks the patch only.
- A `trust: client` or `trust: strict` repository **must** have the block: `bin/repos generate` refuses it
  otherwise (ADR 0032).
- It works with no autonomy ceiling: the check alone holds or lets the push through. A ceiling still applies after it,
  and a ceiling of `act` or `auto` does not lift a refusal.
- A check that cannot run refuses: no `jq`, a setting that is not valid, no patch, no container engine for a
  configured test. It is never skipped.
- A patch refused by the guard never reaches the tests.
- `bin/sandbox-test WORKDIR IMAGE COMMAND` exports the checkout's files (tracked and untracked, not ignored: what
  would be committed, with no `.git`) to a temporary copy and runs the command on it in the container with
  `DOTS_SANDBOX_WRITABLE=1`; the log is capped (`DOTS_SANDBOX_LOG_CAP`, 20 kB). No network means the image carries
  the dependencies.
- The setting reaches a workflow as the input `sandbox`, the base64 of the block's JSON, so that a quote in a command
  cannot break the shell line. `GATE_SANDBOX` and `GATE_WORKDIR` carry it into the gate.

Tests: `bin/test-gate-sandbox` (the gate, with a fake test runner), the e2e cases of `review-pr` and `fix-issue`
(`bin/test-e2e`), and `internal/repos` for the key.

## Which engine on a server

On a server, use **rootless podman**: `DOTS_SANDBOX_ENGINE=podman`.

The reasoning, for the machine this was decided on (a KVM virtual machine of a hosting provider, 4 vCPU,
6 GB, 100 GB):

- **A microVM needs `/dev/kvm`, and a virtual machine usually has none.** The provider does not expose
  nested virtualization (no `/dev/kvm`, no `vmx` or `svm` flag), so Firecracker-style isolation cannot
  run on the machine itself. A microVM then means another machine, which costs money and a trust
  boundary to manage. Check with `ls /dev/kvm` before you plan around one.
- **The machine has room for containers.** CPU, memory and disk are plentiful for a handful of
  short, capped runs, so the weakness of a container is the kernel it shares, not its cost.
- **Rootless over Docker.** A user in the `docker` group is root on the machine, which defeats the point
  of isolating code you do not trust. Rootless podman runs the container as the unprivileged `dots` user,
  with no daemon, so a container escape lands as that user and not as root.
- **It adds one package line**, no service: `apt install podman uidmap crun`. The user needs a range in
  `/etc/subuid` and `/etc/subgid`, which Debian creates with the account.

What this does not give: a kernel escape defeats a container, rootless or not. For the steps that read
text written by strangers and hold a shell (an agent on an issue of a public repository), a container
beside the host is not enough on its own; a microVM on another machine is the stronger choice, see
`ADR/0032-trust-levels-per-repository.md`.

Set it up on the server:

```bash
sudo apt-get install -y podman uidmap crun
podman build -t dots-sandbox-test deploy/sandbox
DOTS_SANDBOX_ENGINE=podman DOTS_TEST_IMAGE=dots-sandbox-test bin/test-sandbox
```

`bin/test-sandbox` runs the real-container checks with the engine named by `DOTS_SANDBOX_ENGINE`, so the
last line proves the isolation on podman: no network, read-only root, no capabilities, memory, process and
time limits, and nothing written to the host.

## How a workflow step calls it by hand

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

- Rootless podman passes `bin/test-sandbox` on Debian 13 with podman 5.4 (crun, overlay). Rootless
  docker is untried. Under rootless, `--user` with the caller's uid and bind-mount ownership may
  behave differently on other versions.
- `gVisor` or any other runtime: not installed, not tried.
- `--cpus` is set but no test measures CPU throttling.
- A kernel or runtime escape: out of scope for a container.
- The container part has not run inside a workflow end to end: the e2e cases use the patch guard
  with a fake test runner (no docker in CI). `fix-ci` and `reply-pr` carry the same wiring as `review-pr` and
  `fix-issue`, without an e2e case of their own yet.
