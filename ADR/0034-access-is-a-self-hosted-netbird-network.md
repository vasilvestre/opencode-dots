# 0034. Access is a self-hosted NetBird network

- Status: accepted
- Date: 2026-11-26
- Supersedes: 0013
- Superseded by: none

## Context

[0013](0013-dashboard-on-the-tailnet.md) publishes the dashboard with
`tailscale serve`. The Tailscale client is open source, but the coordination
server that names the machines and issues the keys is not. The owner wants the
whole access path to be open source, so the control plane must run on a machine
the owner controls.

The need is unchanged. The dashboard reads `state/`: run logs, agent
transcripts, pull request bodies and the price of every run. It has no login, so
it must not answer on a public interface. The operator works from a second
machine and wants the page there without an SSH tunnel on every session.

## Decision

We replace Tailscale with NetBird, and we run NetBird's control plane ourselves
on the same VPS as the dot ([0027](0027-eu-vps-native-local-mode.md)).

- The control plane comes from NetBird's `getting-started.sh`, unattended, in
  Docker Compose with the built-in Traefik. It needs a domain that resolves to
  the VPS, public TCP 443 and UDP 3478, and 2 GB of memory.
- The VPS and the operator's devices install the NetBird client and join with a
  setup key. `NB_MANAGEMENT_URL` points at the VPS.
- The dashboard keeps its `127.0.0.1:8099` bind. Caddy listens on the NetBird
  address of the machine only, on port 8443 (443 belongs to the control plane's
  Traefik), with a certificate from its own local authority for
  `DOTS_DASHBOARD_HOST`, and proxies to loopback.
- `DOTS_ALLOW_HOST` names that same host. The write guard of 0013 is unchanged;
  only the transport that carries the request is.

## Consequences

- The VPS now runs Docker Compose for the control plane. This contradicts the
  "no Docker" line of 0027's MVP. The dot itself still runs native.
- TCP 443 and UDP 3478 answer on the public address. The dashboard does not:
  it stays on loopback, behind Caddy on the NetBird address only.
- Every device on the network can read the state the page reads and call the
  proxied `/awf/` routes. The network is the only gate, as the tailnet was, so
  an untrusted device must not join it.
- Caddy uses its own certificate authority. Each device imports
  `/var/lib/caddy/pki/authorities/local/root.crt` once, or the browser warns.
- The control plane keeps its own database and configuration on the VPS, and it
  needs its own backup.
- A setup key is a secret and stays outside git ([0003](0003-private-config-not-in-git.md)).
- The first NetBird user is created in the browser after the install. The
  setup key used by the script is created there too, so the deployment has one
  manual step per new server.

## Alternatives

- **Keep Tailscale.** It is the setup that runs today, and it is not open
  source end to end.
- **NetBird Cloud.** No control plane to run, but the coordination server is
  again someone else's, which is the point of the change.
- **Publish the dashboard straight on the NetBird address.** It needs no Caddy,
  but it has no name and no certificate, and both the browser and the write
  guard want a name.
- **WireGuard by hand.** No control plane at all, and a key and an allowed-IP
  list to maintain for every device.

## Open questions

- Whether the NetBird client's firewall lets a peer reach the Caddy port on the
  NetBird address without an ACL change.
- Whether NetBird's own reverse proxy can serve the dashboard with a name and a
  certificate, which would remove Caddy.
- How much memory the control plane takes on a 2 GB VPS that already runs the
  dot.
- Whether the setup key can be created from the API during deployment instead
  of by hand in the dashboard after onboarding.
