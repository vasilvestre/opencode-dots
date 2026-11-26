#!/usr/bin/env bash
# NetBird step of the `local` deploy type (ADR 0034): the self-hosted control plane,
# the client, and the Caddy that publishes the dashboard on the NetBird address.
# Needs root. system.sh calls it when NB_CONTROL_DOMAIN or NB_SETUP_KEY is set, and
# it can be run again: each step checks before it changes anything.
#
#   sudo NB_CONTROL_DOMAIN=netbird.example.com NB_LETSENCRYPT_EMAIL=you@example.com \
#        NB_SETUP_KEY=... DOTS_DASHBOARD_HOST=dots.internal deploy/types/local/netbird.sh
#
# Variables:
#   NB_CONTROL_DOMAIN    domain of a self-hosted control plane: installs it here
#   NB_LETSENCRYPT_EMAIL email for its certificate
#   NB_MANAGEMENT_URL    control plane the client joins (default https://$NB_CONTROL_DOMAIN)
#   NB_SETUP_KEY         setup key from the dashboard: joins this machine to the network
#   DOTS_DASHBOARD_HOST  name the dashboard proxy answers on (default dots.internal)
#   DOTS_DASHBOARD_PORT  port of that proxy (default 8443: 443 belongs to the control plane)
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
# shellcheck source=../../lib.sh
. "$here/../../lib.sh"

[ "$(id -u)" = 0 ] || die "run as root (sudo)"

install_control_plane() {
  [ -n "${NB_CONTROL_DOMAIN:-}" ] || return 0
  local dir=/opt/netbird
  if [ -f "$dir/config.yaml" ]; then
    log "netbird control plane: $dir already installed, kept"
    return 0
  fi
  command -v docker >/dev/null 2>&1 || {
    log "docker (the control plane runs in Compose)"
    curl -fsSL https://get.docker.com | sh >/dev/null
  }
  [ -n "${NB_LETSENCRYPT_EMAIL:-}" ] || die "NB_LETSENCRYPT_EMAIL is required to install the control plane"
  install -d -m 700 "$dir"
  log "netbird control plane on $NB_CONTROL_DOMAIN"
  (
    cd "$dir"
    export NETBIRD_DOMAIN="$NB_CONTROL_DOMAIN" NETBIRD_LETSENCRYPT_EMAIL="$NB_LETSENCRYPT_EMAIL" NETBIRD_NON_INTERACTIVE=true
    curl -fsSL https://github.com/netbirdio/netbird/releases/latest/download/getting-started.sh | bash
  )
  log "create the first user and a setup key at https://$NB_CONTROL_DOMAIN, then run this script with NB_SETUP_KEY"
}

join_network() {
  if ! command -v netbird >/dev/null 2>&1; then
    log "netbird client"
    curl -fsSL https://pkgs.netbird.io/install.sh | sh >/dev/null
  fi
  if [ -z "${NB_SETUP_KEY:-}" ]; then
    log "netbird client: NB_SETUP_KEY not set, skipped (create one in the dashboard, then run this script again)"
    return 0
  fi
  local args=(up)
  local url=${NB_MANAGEMENT_URL:-}
  if [ -z "$url" ] && [ -n "${NB_CONTROL_DOMAIN:-}" ]; then
    url="https://$NB_CONTROL_DOMAIN"
  fi
  if [ -n "$url" ]; then args+=(--management-url "$url"); fi
  # NB_SETUP_KEY is read from the environment, so the key never shows in `ps`.
  log "netbird: joining the network"
  netbird "${args[@]}"
}

publish_dashboard() {
  local host=${DOTS_DASHBOARD_HOST:-dots.internal} port=${DOTS_DASHBOARD_PORT:-8443} ip
  ip=$(netbird status --ipv4 2>/dev/null || true)
  # NetBird assigns the interface (wt0) address itself; a peer that changes address
  # needs this script once more so the proxy binds the new one.
  if [ -z "$ip" ]; then
    warn "netbird is not connected: the dashboard proxy is not installed"
    return 0
  fi
  if ! command -v caddy >/dev/null 2>&1; then
    log "caddy"
    apt-get update -qq
    apt-get install -y -qq --no-install-recommends caddy >/dev/null
  fi
  install -d -m 700 /etc/dots
  umask 077
  printf 'DOTS_NETBIRD_IP=%s\nDOTS_DASHBOARD_HOST=%s\nDOTS_DASHBOARD_PORT=%s\n' "$ip" "$host" "$port" >/etc/dots/netbird.env
  install -m 644 "$here/../../netbird/Caddyfile" /etc/caddy/Caddyfile
  install -d /etc/systemd/system/caddy.service.d
  printf '[Unit]\nAfter=netbird.service\nWants=netbird.service\n\n[Service]\nEnvironmentFile=/etc/dots/netbird.env\n' \
    >/etc/systemd/system/caddy.service.d/10-dots.conf
  if [ -d /run/systemd/system ]; then
    systemctl daemon-reload
    systemctl enable caddy >/dev/null 2>&1 || warn "caddy could not be enabled"
    systemctl restart caddy || die "caddy did not start: journalctl -u caddy"
  else
    warn "no systemd here: the Caddy drop-in is written, nothing is started"
  fi
  log "dashboard: https://$host:$port (set DOTS_ALLOW_HOST=$host for writes)"
  log "trust the certificate once per device: /var/lib/caddy/pki/authorities/local/root.crt"
}

install_control_plane
join_network
publish_dashboard
log "netbird step done"
