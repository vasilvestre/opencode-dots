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
#   DOTS_HUB          1 also installs the Mercure hub for claims and events (hub.sh)
#   DOTS_OPENCODE_IDLE  how long an idle project stays loaded in opencode (default: 5 minutes)
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
  for u in dots-opencode.service dots-awf.service dots-dashboard.service dots-tend.service dots-tend.timer dots-workspace-sync.service dots-workspace-sync.timer dots-claim.service dots-claim.timer; do
    [ -f "$src/$u" ] || { warn "$src/$u is missing, skipped"; continue; }
    sed -e "s|%h/projects/perso/opencode-dots|$at|g" \
      -e "s|^ExecStart=/usr/local/bin/awf |ExecStart=%h/.local/bin/awf |" \
      -e "s|=dots\\.service|=dots-awf.service|" "$src/$u" >"$dst/$u"
  done
  mkdir -p "$dst/dots-awf.service.d" "$dst/dots-dashboard.service.d" "$dst/dots-tend.service.d"
  printf '[Unit]\nAfter=dots-opencode.service\nWants=dots-opencode.service\n' >"$dst/dots-awf.service.d/10-after-opencode.conf"
  # Every directory a run touches leaves an instance in the opencode service until it restarts
  # (measured: 147 MB with none, 478 MB after three). A tend pass starts from a fresh service.
  printf '[Service]\nExecStartPre=-/usr/bin/systemctl --user restart dots-opencode.service\nExecStartPre=/bin/sleep 8\n' >"$dst/dots-tend.service.d/10-fresh-opencode.conf"
  # The dashboard lists the models by running `opencode models`, and the installer puts opencode
  # in ~/.opencode/bin, which the user manager's PATH does not have: the list came up empty.
  printf '[Service]\nEnvironment=PATH=%%h/.local/bin:%%h/.opencode/bin:/usr/local/bin:/usr/bin:/bin\n' >"$dst/dots-dashboard.service.d/10-path.conf"
  log "units written to $dst"
}

# A machine with under 1.5 GB of RAM (a 2 GB one reports about 1.9 GB) gets limits, so a runaway run is stopped by systemd and not by
# a machine that thrashes until someone presses its power button. Measured on a 1 GB machine:
# at rest the opencode service holds 448 MB (299 MB resident) out of 962, and a tend pass adds
# 100 to 300 MB. A soft limit (MemoryHigh) made it worse: above it the kernel throttles the
# whole cgroup and reclaims in a loop, which is what froze the first server ("Under memory
# pressure, flushing caches", then a forced power off). So only hard limits are set here, below
# what the machine can give, and the service restarts. 1 GB is tight: 2 GB is the size to take
# if agent runs live on the server. DOTS_MEMORY_PROFILE is small (under 1.5 GB), medium (under 4 GB,
# the opencode service only) or none.
memory_limits() {
  local profile=${DOTS_MEMORY_PROFILE:-auto} total_kb dst="$HOME/.config/systemd/user" name
  total_kb=$(awk '/^MemTotal:/ {print $2}' /proc/meminfo)
  if [ "$profile" = auto ]; then
    if [ "$total_kb" -lt 1500000 ]; then profile=small
    elif [ "$total_kb" -lt 4000000 ]; then profile=medium
    else profile=none; fi
  fi
  # Start from nothing, so a profile that changed (a machine that was resized) leaves no stale cap.
  for name in dots-opencode dots-awf dots-dashboard; do rm -f "$dst/$name.service.d/20-memory.conf"; done
  local caps=
  case "$profile" in
    small) caps="dots-opencode:600M dots-awf:200M dots-dashboard:200M" ;;
    medium) caps="dots-opencode:1200M" ;; # measured: 448 MB at rest, instances pile up, 2 GB machine
    none) ;;
    *) die "DOTS_MEMORY_PROFILE: small, medium or none, got '$profile'" ;;
  esac
  if [ -z "$caps" ]; then log "memory limits: none (profile $profile, ${total_kb} kB)"; return; fi
  local cap max
  for cap in $caps; do
    name=${cap%%:*}
    max=${cap##*:}
    mkdir -p "$dst/$name.service.d"
    printf '[Service]\nMemoryMax=%s\n' "$max" >"$dst/$name.service.d/20-memory.conf"
  done
  log "memory limits written (profile $profile, ${total_kb} kB of RAM)"
}

# opencode keeps a "location" (a project directory and what it loaded: index, watchers) in memory until
# it has been idle for experimental.location_idle_timeout, 60 minutes by default. Measured with
# three directories opened: 428 MB stays at the default, and 242 MB with 1 minute (the floor is
# 147 MB). Five minutes keeps a location loaded across the steps of one tend pass and gives the
# memory back soon after. A config that already exists is never overwritten.
opencode_config() {
  local conf="$HOME/.config/opencode" idle=${DOTS_OPENCODE_IDLE:-5 minutes}
  mkdir -p "$conf"
  if [ -e "$conf/opencode.json" ] || [ -e "$conf/opencode.jsonc" ]; then
    warn "an opencode config exists: add \"experimental\": {\"location_idle_timeout\": \"$idle\"} to it yourself"
    return
  fi
  printf '{\n  "$schema": "https://opencode.ai/config.json",\n  "experimental": { "location_idle_timeout": "%s" }\n}\n' "$idle" >"$conf/opencode.jsonc"
  log "opencode config: idle locations are unloaded after $idle"
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
    if [ -s "$HOME/.config/dots/workspace.env" ]; then
      systemctl --user enable --now dots-workspace-sync.timer
    else
      warn "workspace sync stays off: copy systemd/dots-workspace.env.example to $HOME/.config/dots/workspace.env first"
    fi
    # Nothing to renew until 'bin/claim start' runs, and renewing an empty
    # directory costs one process: enable it now so a claim never lapses.
    systemctl --user enable --now dots-claim.timer
  fi
}

install_awf
install_opencode
clone_repo
render_units
opencode_config
if [ "${DOTS_HUB:-0}" = 1 ]; then DOTS_HOME=$DOTS_HOME "$here/hub.sh"; fi
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
