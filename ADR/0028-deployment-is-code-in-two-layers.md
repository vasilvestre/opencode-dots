# 0028. Deployment is code, in two layers

- Status: proposed
- Date: 2026-11-26
- Supersedes: none
- Superseded by: none

## Context

[0027](0027-eu-vps-native-local-mode.md) puts the dot on one always-on EU VPS.
The first one was set up by hand: create a user, harden SSH, install packages,
enable linger and zram, clone the repository, build `awf`, install `opencode`,
join the tailnet, write the units. None of it is recorded in the repository, so
a second server, a rebuild after a failure, or a move to another provider
starts from memory.

The repository already has a notion of deploy type: `bin/configure` chooses,
for each piece (`opencode`, `awf`, `gh`, `awf-gh`, `claude`), whether it runs
`local` (native binary) or in a `docker` image, and writes `.env` and
`compose.override.yaml` from the answer
([0016](0016-awf-gh-runs-natively.md)). A deployment script that ignored this
choice would duplicate it. Other types are likely later, such as a per-run
sandbox.

Three constraints shape any answer:

- The machine is an ordinary VPS. We cannot count on a permanent root session,
  on `/dev/kvm`, or on nested virtualization. The native `local` type already
  runs without them: the services are user units with linger.
- The provider may change. The first one is Scaleway, chosen for price and
  because its account already exists. OVHcloud, Hetzner and Netcup are
  fallbacks.
- The repository is public. Names, tokens and keys must stay out of it
  ([0003](0003-private-config-not-in-git.md)).

Not deciding leaves a server nobody can rebuild.

## Decision

We keep deployment in the repository, as code. A deployment is described by
two independent choices, and its code is split in two layers that follow them.

**The provider** says where the machine comes from. **The deploy type** says
how the dot runs on it. It is the choice `bin/configure` already owns, so the
type names are its modes. The first supported type is `local`. The `docker`
type stays supported by `bin/configure` and gets no deployment code until
someone needs it.

**Provisioning, optional and per provider.** An OpenTofu module under
`deploy/providers/<provider>/` creates the machine: instance, address, security
group, SSH key. The first one is `deploy/providers/scaleway/`. Its variables,
including the project identifier and zone, live in an ignored `.tfvars` file. A
new provider is a new directory and changes nothing else. Machines created by
hand, or by another tool, skip this layer.

**Bootstrap, required and per deploy type.** Idempotent Bash scripts, in the
style of `bin/` (`set -euo pipefail`, run twice with the same result), turn a
fresh Debian machine into a dot. They live under `deploy/types/<type>/` and are
split by the privilege they need. For the `local` type:

- `system` needs sudo and runs once. It installs packages, creates the user,
  hardens SSH, enables linger and zram, and joins the tailnet.
- `user` needs no root. It installs `awf`, `opencode` and tower, clones the
  repository to a generic directory, installs the user units and timers, and
  starts them. The directory is `~/dots` by default and is set once, as
  `DOTS_HOME`. The unit files in `systemd/` name the workstation's path, so the
  script writes copies of them with the chosen path, as the README already does
  with `sed`. The units in `systemd/` do not change. `STATE_DIR` and `WORK_ROOT`
  sit under that directory and are absolute, as the `local` type requires.

The scripts do not decide which piece runs where. They install what the type
needs, then call `bin/configure` with the flags of that type, so the mode matrix
has one owner. Nothing in the `local` type needs Docker, a hypervisor, or a root
service. A machine on which someone else already did the system step can run
the user step alone.

The bootstrap pins the versions it installs, and the pins are reviewed in a
diff. `opencode` is on major version 2, validated with 2.4.1, and `awf` is
`v0.12.0`, the first release that can preflight `opencode` v2. The default
installer URL installs major version 1 (1.19.6 was offered on the first
server), and the v2 installer is a different URL, `https://opencode.ai/v2/install`
(2.4.2 on the first server). The script uses the v2 URL. After the install it
checks the major version and stops if it is not 2.

The bootstrap compiles nothing on the server. Building `awf` with `go install`
froze the first server (1 vCPU, 1 GB). `awf` v0.12.0 and tower v0.1.0 publish
Linux release archives for `x86_64` and `aarch64`, and the script downloads
them and checks the published checksum before it installs anything. A failed
check stops the script.

Tower is part of the bootstrap. The README still describes a build from source
for tower and says its `agent_memory` extension is not copied by the install.
Whether the release archive carries that extension is not checked yet.

Secrets and private files never enter the repository. The tailnet auth key,
`GH_TOKEN` and provider credentials are read from the environment or asked for
at run time. `repos.yaml` and `.env` are copied to the server by a command that
uses the tailnet. The scripts write no secret to a file under version control
or to a log.

The OpenTofu state stays local and is encrypted. Its encryption key is not in
the repository, and the state file is ignored by git.

We write the scripts from a manual run that worked, not before. The first
version of each script records the steps of the first server, and a test runs
both inside a Debian 13 container, which needs no KVM, before a real server is
touched.

## Consequences

- A second server, or a rebuild, is a command and a few secrets, not a day of
  memory.
- The steps are reviewable: a change to the install is a diff.
- A new deploy type or provider is a new directory. The two choices do not
  multiply files, because each is written once.
- The user step runs without root, so a restricted host can still run the dot.
- The tailnet is still the only gate, and the system script is the place where
  that rule is enforced, so a mistake there is a mistake everywhere.
- Two layers cost more files than one script. We accept it for the provider and
  type split.
- Each script is tied to Debian at first. Another distribution is a later
  change.
- A local, encrypted state keeps the machine addresses and identifiers off the
  repository, and it makes the key a thing to keep and back up: losing it loses
  the state.
- A bootstrap that installs from the network (the `opencode` installer, the
  `awf` and tower release archives) depends on those sources staying available.
  The checksum check guards against a corrupted download, not against a release
  that was replaced upstream.

## Alternatives

- **Ansible, as the tochiro repository does.** Roles and inventories suit
  several servers. It adds a dependency on the machine that runs it, and we have
  one server. Bash needs nothing on the target. We can revisit it if the count
  grows.
- **cloud-init only.** It runs at first boot and cannot be run again, so it
  fixes neither a drift nor a rebuild of part of the machine. It stays useful as
  a way to start the system script.
- **OpenTofu with remote-exec.** It ties the install to one provider and to a
  state file, so a hand-made machine could not use it.
- **A runbook in the README.** Cheap, and it is what the first server needed.
  It cannot be tested and it drifts.
- **Deploy a container image.** [0016](0016-awf-gh-runs-natively.md) took the
  image out of the polling path, and a container brings back the rebuild and
  the token-bearing build.
- **Organise by provider alone.** It leaves the deploy type implicit and
  copies the mode choice of `bin/configure` into every provider directory.

## Open questions

- Where the tailnet auth key comes from at run time, and its lifetime.
- Whether to support a distribution other than Debian 13 in the first version.
- Whether the tower release archive carries the `agent_memory` extension.
- Whether the Go builds that remain (`bin/repos`, the dashboard) are light enough
  for the smallest machine, or should also come as binaries.
- How the state encryption key is stored and backed up.
