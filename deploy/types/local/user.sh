#!/usr/bin/env bash
# User step of the `local` deploy type (ADR 0028). Needs no root: run it as the user that
# runs the dot, after system.sh. It can be run again; each step checks first.
#
#   deploy/types/local/user.sh
#
# It compiles nothing. awf comes as a release binary whose checksum is verified. opencode
# comes from its v2 installer. tower, bin/repos and bin/dashboard are pushed from a
# workstation by push.sh.
#
# Variables (all optional):
#   DOTS_HOME         where the repository goes (default: ~/dots)
#   DOTS_REPO         what to clone (default: the repository named in versions.env)
#   DOTS_ENABLE_TEND  1 enables the daily tend timer once tend.list exists
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
# shellcheck source=../../lib.sh
. "$here/../../lib.sh"
# shellcheck source=../../versions.env
. "$here/../../versions.env"

[ "$(id -u)" != 0 ] || die "run as the user that runs the dot, not as root"
: "${DOTS_HOME:=$HOME/dots}"
: "${DOTS_REPO:=$DOTS_REPO_DEFAULT}"
export PATH="$HOME/.local/bin:$HOME/.opencode/bin:$PATH"
mkdir -p "$HOME/.local/bin" "$HOME/.config/systemd/user"

install_awf() {
  if command -v awf >/dev/null 2>&1 && awf --version 2>/dev/null | grep -q "${AWF_VERSION#v}"; then
    log "awf ${AWF_VERSION}: already installed"
    return
  fi
  local arch tmp base
  arch=$(awf_arch)
  base="https://github.com/awf-project/cli/releases/download/$AWF_VERSION"
  tmp=$(mktemp -d)
  # shellcheck disable=SC2064
  trap "rm -rf '$tmp'" RETURN
  log "awf $AWF_VERSION ($arch)"
  curl -fsSL -o "$tmp/awf.tgz" "$base/awf_linux_$arch.tar.gz"
  curl -fsSL -o "$tmp/checksums.txt" "$base/checksums.txt"
  (cd "$tmp" && grep " awf_linux_$arch.tar.gz\$" checksums.txt | sed "s| awf_linux_$arch.tar.gz\$| awf.tgz|" | sha256sum -c - >/dev/null) ||
    die "checksum of awf_linux_$arch.tar.gz does not match, nothing installed"
  tar -xzf "$tmp/awf.tgz" -C "$tmp" awf
  install -m 755 "$tmp/awf" "$HOME/.local/bin/awf"
}

install_opencode() {
  local major=
  if command -v opencode >/dev/null 2>&1; then
    major=$(opencode --version 2>/dev/null | sed -n 's/^[^0-9]*\([0-9][0-9]*\)\..*/\1/p' | head -1)
  fi
  if [ "$major" = "$OPENCODE_MAJOR" ]; then
    log "already installed: $(opencode --version)"
    return
  fi
  log "opencode (installer: $OPENCODE_INSTALL_URL)"
  curl -fsSL "$OPENCODE_INSTALL_URL" | bash >/dev/null
  major=$("$HOME/.opencode/bin/opencode" --version 2>/dev/null | sed -n 's/^[^0-9]*\([0-9][0-9]*\)\..*/\1/p' | head -1)
  [ "$major" = "$OPENCODE_MAJOR" ] || die "opencode major version is '$major', expected $OPENCODE_MAJOR"
}

clone_repo() {
  if [ -d "$DOTS_HOME/.git" ]; then
    log "repository: updating $DOTS_HOME"
    git -C "$DOTS_HOME" pull --ff-only --quiet
  else
    log "repository: cloning into $DOTS_HOME"
    git clone --quiet "$DOTS_REPO" "$DOTS_HOME"
  fi
  mkdir -p "$HOME/.config/awf"
  ln -sfn "$DOTS_HOME/workflows" "$HOME/.config/awf/workflows"
}

# The units in systemd/ name the workstation's path and /usr/local/bin/awf. Write copies
# that name this machine's, and leave the originals alone.
render_units() {
  local at rel
  case "$DOTS_HOME" in
    "$HOME"/*) rel=${DOTS_HOME#"$HOME"/}; at="%h/$rel" ;;
    *) at=$DOTS_HOME ;;
  esac
  local src="$DOTS_HOME/systemd" dst="$HOME/.config/systemd/user" u
  for u in dots-opencode.service dots-awf.service dots-dashboard.service dots-tend.service dots-tend.timer; do
    [ -f "$src/$u" ] || { warn "$src/$u is missing, skipped"; continue; }
    sed -e "s|%h/projects/perso/opencode-dots|$at|g" \
      -e "s|^ExecStart=/usr/local/bin/awf |ExecStart=%h/.local/bin/awf |" \
      -e "s|=dots\\.service|=dots-awf.service|" "$src/$u" >"$dst/$u"
  done
  mkdir -p "$dst/dots-awf.service.d" "$dst/dots-dashboard.service.d"
  printf '[Unit]\nAfter=dots-opencode.service\nWants=dots-opencode.service\n' >"$dst/dots-awf.service.d/10-after-opencode.conf"
  # The dashboard lists the models by running `opencode models`, and the installer puts opencode
  # in ~/.opencode/bin, which the user manager's PATH does not have: the list came up empty.
  printf '[Service]\nEnvironment=PATH=%%h/.local/bin:%%h/.opencode/bin:/usr/local/bin:/usr/bin:/bin\n' >"$dst/dots-dashboard.service.d/10-path.conf"
  log "units written to $dst"
}

# A machine with under 2 GB of RAM (measured on a 1 GB one: the opencode service holds about
# 300 MB at rest and a tend pass adds about 200 MB) gets limits, so a runaway run is stopped
# by systemd and not by the kernel's OOM killer taking the SSH session with it.
# DOTS_MEMORY_PROFILE=small forces them, none turns them off.
memory_limits() {
  local profile=${DOTS_MEMORY_PROFILE:-auto} total_kb dst="$HOME/.config/systemd/user"
  total_kb=$(awk '/^MemTotal:/ {print $2}' /proc/meminfo)
  if [ "$profile" = auto ]; then
    if [ "$total_kb" -lt 2000000 ]; then profile=small; else profile=none; fi
  fi
  [ "$profile" = small ] || { log "memory limits: none ($profile, ${total_kb} kB)"; return; }
  local unit high max
  for unit in dots-opencode:600M:750M dots-awf::200M dots-dashboard::200M; do
    IFS=: read -r name high max <<<"$unit"
    mkdir -p "$dst/$name.service.d"
    {
      echo "[Service]"
      [ -z "$high" ] || echo "MemoryHigh=$high"
      echo "MemoryMax=$max"
    } >"$dst/$name.service.d/20-memory.conf"
  done
  log "memory limits written (profile small, ${total_kb} kB of RAM)"
}

start_services() {
  if ! have_systemd_user; then
    warn "no systemd user manager here: units are written, nothing is started"
    return
  fi
  systemctl --user daemon-reload
  systemctl --user enable --now dots-opencode.service
  local i
  for i in $(seq 1 30); do
    [ -f "$HOME/.local/state/opencode/service.json" ] && break
    sleep 1
  done
  [ -f "$HOME/.local/state/opencode/service.json" ] || warn "opencode did not write service.json in 30 s"
}

configure_dots() {
  if [ ! -f "$HOME/.local/state/opencode/service.json" ]; then
    warn "bin/configure skipped: opencode service is not running"
    return
  fi
  if ! gh auth status >/dev/null 2>&1; then
    warn "bin/configure skipped: log in first with 'gh auth login --with-token' (a token limited to the repositories this machine may touch), then run this script again"
    return
  fi
  (cd "$DOTS_HOME" && bin/configure --opencode=local --awf=local --gh=local >/dev/null && chmod 600 .env)
  log "bin/configure done (.env written, mode 600)"
  if have_systemd_user; then
    systemctl --user enable --now dots-awf.service
    if [ -x "$DOTS_HOME/bin/dashboard" ]; then systemctl --user enable --now dots-dashboard.service; fi
    if [ "${DOTS_ENABLE_TEND:-0}" = 1 ] && [ -s "$DOTS_HOME/tend.list" ]; then
      systemctl --user enable --now dots-tend.timer
    fi
  fi
}

install_awf
install_opencode
clone_repo
render_units
memory_limits
start_services
configure_dots

missing=
for f in bin/repos bin/dashboard; do [ -x "$DOTS_HOME/$f" ] || missing="$missing $f"; done
command -v tower >/dev/null 2>&1 || missing="$missing tower"
if [ -n "$missing" ]; then
  log "still to push from a workstation:$missing (deploy/types/local/push.sh <user@host> [--tower])"
fi
log "user step done"
