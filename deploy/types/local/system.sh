#!/usr/bin/env bash
# System step of the `local` deploy type (ADR 0028). Needs root, runs once on a fresh
# Debian machine, and can be run again: each step checks before it changes anything.
#
#   sudo DOTS_SSH_PUBKEY="$(cat ~/.ssh/id_ed25519.pub)" deploy/types/local/system.sh
#
# Variables (all optional):
#   DOTS_USER           user that runs the dot (default: dots)
#   DOTS_SSH_PUBKEY     public key put in that user's authorized_keys
#   DOTS_SUDO_NOPASSWD  1 gives the user passwordless sudo (the account has no password)
#   DOTS_HARDEN_SSH     1 turns off root login and passwords for SSH. Needs a key for the
#                       user. Test a second session before you close the first one.
#   TS_AUTHKEY          Tailscale auth key: installs Tailscale and joins the tailnet
#   TS_HOSTNAME         name of the machine on the tailnet
#   TS_TAGS             tags to advertise, for example tag:dots
#   DOTS_TS_SERVE_PORT  publish the dashboard (127.0.0.1:8099) on that HTTPS tailnet port
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
# shellcheck source=../../lib.sh
. "$here/../../lib.sh"

[ "$(id -u)" = 0 ] || die "run as root (sudo)"
: "${DOTS_USER:=dots}"

log "packages"
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq --no-install-recommends ca-certificates curl git jq make sudo gh zram-tools >/dev/null

log "user $DOTS_USER"
if ! id "$DOTS_USER" >/dev/null 2>&1; then
  useradd --create-home --shell /bin/bash "$DOTS_USER"
fi
home=$(getent passwd "$DOTS_USER" | cut -d: -f6)
if [ -n "${DOTS_SSH_PUBKEY:-}" ]; then
  install -d -m 700 -o "$DOTS_USER" -g "$DOTS_USER" "$home/.ssh"
  touch "$home/.ssh/authorized_keys"
  grep -qxF "$DOTS_SSH_PUBKEY" "$home/.ssh/authorized_keys" || printf '%s\n' "$DOTS_SSH_PUBKEY" >>"$home/.ssh/authorized_keys"
  chown "$DOTS_USER:$DOTS_USER" "$home/.ssh/authorized_keys"
  chmod 600 "$home/.ssh/authorized_keys"
fi
if [ "${DOTS_SUDO_NOPASSWD:-0}" = 1 ]; then
  f=/etc/sudoers.d/90-$DOTS_USER
  printf '%s ALL=(ALL) NOPASSWD:ALL\n' "$DOTS_USER" >"$f.tmp"
  visudo -cf "$f.tmp" >/dev/null || { rm -f "$f.tmp"; die "sudoers entry refused by visudo"; }
  chmod 440 "$f.tmp"
  mv "$f.tmp" "$f"
fi

if [ -d /run/systemd/system ]; then
  log "linger and zram"
  loginctl enable-linger "$DOTS_USER"
  printf 'ALGO=zstd\nPERCENT=50\n' >/etc/default/zramswap
  systemctl restart zramswap || warn "zramswap did not restart"
  if systemctl is-enabled systemd-resolved >/dev/null 2>&1; then
    # LLMNR listens on every interface and a server has no use for it.
    install -d /etc/systemd/resolved.conf.d
    printf '[Resolve]\nLLMNR=no\n' >/etc/systemd/resolved.conf.d/10-no-llmnr.conf
    systemctl restart systemd-resolved
  fi
else
  warn "systemd is not running: linger, zram and resolved are left alone"
fi

if [ "${DOTS_HARDEN_SSH:-0}" = 1 ]; then
  if [ ! -s "$home/.ssh/authorized_keys" ]; then
    die "DOTS_HARDEN_SSH needs a key in $home/.ssh/authorized_keys, or you would lock yourself out"
  fi
  if [ -d /etc/ssh/sshd_config.d ] && command -v sshd >/dev/null 2>&1; then
    log "ssh: no root login, no passwords"
    printf 'PermitRootLogin no\nPasswordAuthentication no\n' >/etc/ssh/sshd_config.d/10-hardening.conf
    sshd -t || { rm -f /etc/ssh/sshd_config.d/10-hardening.conf; die "sshd refused the configuration, removed it"; }
    systemctl reload ssh 2>/dev/null || systemctl reload sshd 2>/dev/null || warn "could not reload sshd"
  else
    warn "no sshd here: nothing to harden"
  fi
fi

if [ -n "${TS_AUTHKEY:-}" ]; then
  log "tailscale"
  if ! command -v tailscale >/dev/null 2>&1; then
    curl -fsSL https://tailscale.com/install.sh | sh >/dev/null
  fi
  args=(up --auth-key="$TS_AUTHKEY")
  [ -n "${TS_HOSTNAME:-}" ] && args+=(--hostname="$TS_HOSTNAME")
  [ -n "${TS_TAGS:-}" ] && args+=(--advertise-tags="$TS_TAGS")
  tailscale "${args[@]}"
  if [ -n "${DOTS_TS_SERVE_PORT:-}" ]; then
    tailscale serve --bg --https="$DOTS_TS_SERVE_PORT" http://127.0.0.1:8099
  fi
else
  log "tailscale: TS_AUTHKEY not set, skipped (join the tailnet yourself, then close SSH to the world)"
fi

log "system step done; next, as $DOTS_USER: deploy/types/local/user.sh"
