#!/usr/bin/env bash
# Installs the Mercure hub for claims and events (ADR 0026), as a user service. Needs no root.
# It can be run again. user.sh calls it when DOTS_HUB=1.
#
#   deploy/types/local/hub.sh
#
# The hub is a release binary whose checksum is verified. Its two keys are generated here, on the
# machine that runs it, into ~/.config/dots/hub.env (mode 600); they are not in the repository and
# not printed. Clients get the key by a channel you choose (`ssh host 'cat ~/.config/dots/hub.env'`).
# The hub listens on 127.0.0.1:3901: reach it by an SSH tunnel, or publish it on the tailnet with
# `sudo tailscale serve --bg --https=8445 http://127.0.0.1:3901`.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
# shellcheck source=../../lib.sh
. "$here/../../lib.sh"
# shellcheck source=../../versions.env
. "$here/../../versions.env"

[ "$(id -u)" != 0 ] || die "run as the user that runs the dot, not as root"
: "${DOTS_HOME:=$HOME/dots}"
env_file=$HOME/.config/dots/hub.env
mkdir -p "$HOME/.local/bin" "$HOME/.config/systemd/user" "$HOME/.config/dots"

install_hub() {
  if command -v mercure >/dev/null 2>&1 && mercure version 2>/dev/null | grep -q "${MERCURE_VERSION#v}"; then
    log "mercure $MERCURE_VERSION: already installed"
    return
  fi
  local arch tmp base
  case "$(uname -m)" in
    x86_64) arch=x86_64 ;;
    aarch64 | arm64) arch=arm64 ;;
    *) die "unsupported architecture: $(uname -m)" ;;
  esac
  base="https://github.com/dunglas/mercure/releases/download/$MERCURE_VERSION"
  tmp=$(mktemp -d)
  # shellcheck disable=SC2064
  trap "rm -rf '$tmp'" RETURN
  log "mercure $MERCURE_VERSION ($arch)"
  curl -fsSL -o "$tmp/hub.tgz" "$base/mercure_Linux_$arch.tar.gz"
  curl -fsSL -o "$tmp/checksums.txt" "$base/checksums.txt"
  (cd "$tmp" && grep " mercure_Linux_$arch.tar.gz\$" checksums.txt | sed "s| mercure_Linux_$arch.tar.gz\$| hub.tgz|" | sha256sum -c - >/dev/null) ||
    die "checksum of mercure_Linux_$arch.tar.gz does not match, nothing installed"
  tar -xzf "$tmp/hub.tgz" -C "$tmp" mercure
  install -m 755 "$tmp/mercure" "$HOME/.local/bin/mercure"
}

write_env() {
  if [ -s "$env_file" ]; then
    log "keys: $env_file already exists, kept"
    return
  fi
  local key
  key=$(head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n')
  umask 077
  cat >"$env_file" <<ENV
# Written by deploy/types/local/hub.sh. Secret: never commit it.
MERCURE_PUBLISHER_JWT_KEY=$key
MERCURE_PUBLISHER_JWT_ALG=HS256
MERCURE_SUBSCRIBER_JWT_KEY=$key
MERCURE_SUBSCRIBER_JWT_ALG=HS256
MERCURE_TRUSTED_ISSUERS=dots
SERVER_NAME=http://:3901
ENV
  chmod 600 "$env_file"
  log "keys written to $env_file (mode 600)"
}

write_unit() {
  local at rel
  case "$DOTS_HOME" in
    "$HOME"/*) rel=${DOTS_HOME#"$HOME"/}; at="%h/$rel" ;;
    *) at=$DOTS_HOME ;;
  esac
  [ -f "$DOTS_HOME/systemd/dots-hub.service" ] || die "$DOTS_HOME/systemd/dots-hub.service is missing: update the repository first"
  sed "s|%h/projects/perso/opencode-dots|$at|g" "$DOTS_HOME/systemd/dots-hub.service" >"$HOME/.config/systemd/user/dots-hub.service"
  if [ "$(awk '/^MemTotal:/ {print $2}' /proc/meminfo)" -lt 1500000 ]; then
    mkdir -p "$HOME/.config/systemd/user/dots-hub.service.d"
    printf '[Service]\nMemoryMax=150M\n' >"$HOME/.config/systemd/user/dots-hub.service.d/20-memory.conf"
  fi
}

install_hub
write_env
write_unit
if have_systemd_user; then
  systemctl --user daemon-reload
  systemctl --user enable --now dots-hub.service
  sleep 3
  if curl -fsS -o /dev/null http://127.0.0.1:3901/; then log "hub answers on 127.0.0.1:3901"; else warn "the hub did not answer on 127.0.0.1:3901: systemctl --user status dots-hub"; fi
else
  warn "no systemd user manager here: the unit is written, nothing is started"
fi
log "hub step done"
