#!/usr/bin/env bash
# Runs inside the container started by run.sh: the system step as root, then the user
# step as the new user, each twice (they must be idempotent), then checks the result.
# There is no systemd here, so the scripts write their files and skip enabling them.
set -euo pipefail

fail=0
check() { # check <description> <command...>
  local d=$1
  shift
  if "$@" >/dev/null 2>&1; then echo "ok    $d"; else echo "FAIL  $d"; fail=1; fi
}

export DEBIAN_FRONTEND=noninteractive
# A fixed test key: not a secret, it only has to look like one.
key='ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIDeployTestKeyDeployTestKeyDeployTestKey1 test'

echo "== system step, twice"
DOTS_USER=dots DOTS_SSH_PUBKEY="$key" DOTS_SUDO_NOPASSWD=1 bash /src/deploy/types/local/system.sh
DOTS_USER=dots DOTS_SSH_PUBKEY="$key" DOTS_SUDO_NOPASSWD=1 bash /src/deploy/types/local/system.sh
check "user exists" id dots
check "key installed once" test "$(grep -cF "$key" /home/dots/.ssh/authorized_keys)" = 1
check "authorized_keys mode 600" test "$(stat -c %a /home/dots/.ssh/authorized_keys)" = 600
check "sudoers file valid" visudo -cf /etc/sudoers.d/90-dots
check "gh installed" command -v gh
check "jq installed" command -v jq

echo "== hardening refuses without a key"
useradd -m nokey
if DOTS_USER=nokey DOTS_HARDEN_SSH=1 bash /src/deploy/types/local/system.sh >/dev/null 2>&1; then
  echo "FAIL  hardening ran for a user without a key"
  fail=1
else
  echo "ok    hardening refused for a user without a key"
fi

echo "== user step, twice"
su - dots -c "git config --global safe.directory '*'"
run_user() { su - dots -c "DOTS_REPO=/src DOTS_MEMORY_PROFILE=small DOTS_HUB=1 bash /src/deploy/types/local/user.sh"; }
run_user
first_keys=$(su - dots -c "md5sum < ~/.config/dots/hub.env")
run_user
run_as_dots() { su - dots -c "export PATH=\$HOME/.local/bin:\$HOME/.opencode/bin:\$PATH; $*"; }
check "awf 0.12.0" run_as_dots 'awf --version | grep -q 0.12.0'
check "opencode major 2" run_as_dots 'opencode --version | grep -q "v2\\."'
check "repository cloned" test -d /home/dots/dots/.git
check "workflows linked for awf" test -L /home/dots/.config/awf/workflows
for u in dots-opencode.service dots-awf.service dots-dashboard.service dots-tend.service dots-tend.timer; do
  check "unit $u written" test -f "/home/dots/.config/systemd/user/$u"
done
check "units carry no workstation path" test "$(grep -rl 'projects/perso' /home/dots/.config/systemd/user | wc -l)" = 0
check "awf unit runs the user's awf" grep -q '^ExecStart=%h/.local/bin/awf ' /home/dots/.config/systemd/user/dots-awf.service
check "units use the chosen directory" grep -q '%h/dots' /home/dots/.config/systemd/user/dots-awf.service
check "dashboard waits for dots-awf" grep -q '^After=dots-awf.service' /home/dots/.config/systemd/user/dots-dashboard.service
check "awf starts after opencode" grep -q 'dots-opencode.service' /home/dots/.config/systemd/user/dots-awf.service.d/10-after-opencode.conf
check "opencode service limited to 600M" grep -q '^MemoryMax=600M' /home/dots/.config/systemd/user/dots-opencode.service.d/20-memory.conf
check "no soft limit that makes the kernel thrash" test "$(grep -c MemoryHigh /home/dots/.config/systemd/user/dots-opencode.service.d/20-memory.conf)" = 0
check "awf limited to 200M" grep -q '^MemoryMax=200M' /home/dots/.config/systemd/user/dots-awf.service.d/20-memory.conf
check "dashboard finds opencode on its PATH" grep -q ".opencode/bin" /home/dots/.config/systemd/user/dots-dashboard.service.d/10-path.conf
check "hub binary installed" run_as_dots 'mercure version | grep -q 1.1'
check "hub keys are private" test "$(stat -c %a /home/dots/.config/dots/hub.env)" = 600
check "hub keys survive a second run" test "$first_keys" = "$(su - dots -c 'md5sum < ~/.config/dots/hub.env')"
check "hub keys are not the empty string" grep -Eq '^MERCURE_PUBLISHER_JWT_KEY=[0-9a-f]{64}$' /home/dots/.config/dots/hub.env
check "hub unit points at the repository's Caddyfile" grep -q '%h/dots/deploy/hub/Caddyfile' /home/dots/.config/systemd/user/dots-hub.service
check "no secret file written" test ! -e /home/dots/dots/.env

if [ "$fail" = 0 ]; then echo "deploy test: all checks passed"; else echo "deploy test: failures above" >&2; exit 1; fi
