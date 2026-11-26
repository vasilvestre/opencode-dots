# 0027. The dot runs on one EU VPS in native local mode

- Status: proposed
- Date: 2026-11-26
- Supersedes: none
- Superseded by: none

## Context

Today the dot runs on a personal workstation. When it is off, nothing handles
the repositories. We want them handled around the clock. The cloud machine is
authoritative and the workstation becomes a working view
([0023](0023-tracker-independent-work-item.md),
[0024](0024-control-plane-database.md)).

The install already has a mode that fits a server. [0016](0016-awf-gh-runs-natively.md)
lets every piece run natively (`bin/configure --awf=local --awf-gh=local`), so no
image, no container and no downloaded binary sit in the polling path. The
network rule is also settled: nothing binds a public interface, and the tailnet
reaches the dashboard through `tailscale serve`
([0013](0013-dashboard-on-the-tailnet.md)). Credentials stay out of git
([0003](0003-private-config-not-in-git.md)).

The first projects are open source. Non-EU models are accepted for them. The
machine itself should still be in the EU, for latency and as a GDPR minimum.

Not deciding keeps the dot tied to one laptop's uptime.

## Decision

We run the dot on one always-on VPS in the EU, in the existing native `local`
mode, not in docker mode. On that machine run `awf-gh`, `awf`, tower, the
dashboard and `opencode serve`, and later the database and the event hub.

- **Network.** Nothing binds a public interface. Access is the tailnet only,
  as in 0013. Ports stay on `127.0.0.1`.
- **Services.** The user units run without a login session: we enable
  `loginctl enable-linger` for the dot's user.
- **Memory.** The target is 4 GB. We enable zram, set `MemoryMax` on the
  units, and limit concurrent runs to one or two.
- **Credentials.** Claude is connected once, on the VPS. Other devices attach
  to that server with `opencode --server <URL>`. The flag exists in v2.4.1; its
  authentication is untested. No full provider key sits on a device. `GH_TOKEN`
  and provider credentials stay on the VPS only.
- **Local work.** A device may still work on its own with a restricted key, for
  example one limited to two or three models. Local mode then does everything
  except the centralized tasks (tend, polling). Whether a key can be limited
  this way is unverified.
- **Backups.** We back up `state/` and the database.

### MVP scope

The MVP is small and local first. We start by working locally, and add the
cloud machine when it is needed. The cloud machine is one micro VM at low cost.
It runs the core (opencode-dots, awf, tower) and the tend job. The dashboard,
the database and the event hub come later, when their records are accepted.
Sizing for the MVP follows the micro VM, so the 4 GB figure above is a ceiling
to check, not a requirement.

For the MVP the VPS runs Debian stable, minimal, with no Docker and no
desktop. Only the tend job runs there. Every other run stays on the
workstation at first, so one run at a time on the VPS is enough. We still
enable zram and a small swap, set `MemoryMax`, and use shallow clones.

### Choosing a provider

The choice is open. The criteria:

- location in the EU;
- price for 4 GB (sources give about 4 to 5.5 EUR per month and disagree; to
  confirm);
- the IPv4 fee;
- VAT;
- backups, and what they cost;
- a stable network.

Candidates found: OVHcloud, Hetzner, Netcup, Scaleway.

The owner chose Scaleway Stardust for the MVP. It is the smallest, cheapest
instance type. We have not confirmed its memory, price, IPv4 fee, VAT or
backup options, and this record gives no figures for it. The other candidates
stay as fallbacks if Stardust proves too small.

### Not covered

- Nested virtualization and a sandbox per run. Hetzner Cloud and Infomaniak do
  not expose it. Bare metal such as Scaleway Elastic Metal starts around 33 EUR
  per month. All of this is unconfirmed.
- Trust levels. A later record.
- A gateway for LLM keys. Deferred: Claude goes through the SDK inside
  opencode and bypasses a gateway.

## Consequences

- Runs continue while the workstation is off.
- One machine to maintain: updates, disk, backups, restarts.
- One more attack surface. The tailnet is the only gate, and every device on it
  inherits access (0013). The VPS holds all the credentials, so its compromise
  is the worst case.
- Polling every 2 minutes still sets the latency. A VPS does not change that.
- A 4 GB machine caps parallelism. A burst of events queues.
- The workstation needs no provider key. It loses nothing else.
- Docker mode stays supported but is not the target here.

## Alternatives

- **Keep the workstation.** Free, but runs stop when it sleeps or is off.
- **A home server.** Same uptime goal, but depends on a home line and power,
  and is not in a datacenter.
- **Docker mode on the VPS.** Brings back the image, its rebuild and a
  token-bearing build that 0016 removed.
- **Serverless sandboxes such as Fly Sprites.** Strong isolation per run, but
  the control plane still needs a home, and the cost and model are unproven.
- **GitHub Actions as router.** Removes the poll delay, but ties the dot to one
  tracker and to Actions minutes, against 0023.

## Open questions

- Whether Stardust (1 vCPU, 1 GB, 10 GB, as stated by the owner) is enough for awf, tower and tend, or a larger type is needed. Disk for clones is the tight part.
- The confirmed memory, price, IPv4 fee, VAT and backups for Scaleway Stardust, and the EU region to use.
- Whether `opencode --server` authentication holds up from other devices.
- Which backup target, and how often.
- How many concurrent runs 4 GB really allows.
- Whether opencode go or the provider can restrict a key to a few models, or
  cap its spend.
- How claims ([0026](0026-claims-and-presence.md)) stop a local run and a VPS run from
  handling the same work item.
