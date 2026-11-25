# 0013. The dashboard answers on the tailnet through tailscale serve

- Status: accepted
- Date: 2026-11-23
- Supersedes: none
- Superseded by: none

## Context

The dashboard binds to the loopback interface, and the project rule says no
port leaves loopback. That rule protects a page that reads `state/`: run logs,
agent transcripts, pull request bodies, thread files and the price of every
run. Opening the listen address would publish all of it to the local network
with no authentication at all.

The operator also works from a second machine and wants the same page there
without an SSH tunnel on every session.

## Decision

The dashboard keeps its loopback bind, and `tailscale serve` publishes it to
the tailnet over HTTPS. The published address is the machine's tailnet name on
a dedicated port, and it is reachable from tailnet devices only.

The tailnet provides TLS, a stable name and a per-device identity check. The
dashboard gains no listener on any external interface.

Writes keep their guard and gain one exception: an explicit allow-list of extra
hosts, set through configuration, whose requests may write. Every other host
stays refused, so the JSON content type check and the timestamped backup still
stand, and a browser on a foreign site cannot reach the write endpoints.

## Consequences

The operator opens the real page from the second machine with no tunnel. The
traffic is encrypted and limited to devices on the tailnet.

Every tailnet device can read the state the page reads, and can call the
`/awf/` proxy, which forwards any method: cancelling a run, resuming one, or
starting one. The tailnet is the only gate. Any device added to it inherits that
access, so an untrusted device must not join the tailnet.

The allowed host is configuration, not code. Renaming the machine or moving it
to another tailnet makes writes fail until the allow-list follows.

## Alternatives

Listen on all interfaces and filter with the host firewall. It publishes
plaintext over the local network, and any device behind the same router can
read the transcripts.

Listen on the tailscale interface address directly. It works, but the
certificate and the name come from the tailnet anyway, so serving through the
daemon gives the same result with less to configure.

Keep loopback only, and forward the port over SSH from the second machine. It
costs a tunnel per session, and the page is read most often from that machine.
