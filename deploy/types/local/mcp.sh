#!/usr/bin/env bash
# Adds the tower memory MCP to the opencode config of a server and restarts opencode.
# Only tower: the other MCP servers of a workstation hold personal keys and never go
# to a server. Run it on the workstation. It can be run again: a second run changes
# nothing and says so.
#
#   deploy/types/local/mcp.sh dots@server [--remote-dir DIR]
#
# Needs tower on the server (push.sh --tower), and jq. The config must be plain JSON:
# a file with // comments is refused and left alone. --remote-dir is accepted so the
# scripts take the same arguments; this one does not use it.
set -euo pipefail
cd "$(dirname "$0")/../../.."
# shellcheck source=../../lib.sh
. deploy/lib.sh

[ $# -ge 1 ] || die "usage: mcp.sh user@host [--remote-dir DIR]"
host=$1
shift
while [ $# -gt 0 ]; do
  case "$1" in
    --remote-dir) : "${2:?--remote-dir needs a directory}"; shift ;;
    *) die "unknown argument: $1" ;;
  esac
  shift
done

ssh_opts=(-o BatchMode=yes)
log "tower MCP on $host"
# The function travels with `declare -f`, so the server runs exactly what user.sh runs.
set +e
out=$(ssh "${ssh_opts[@]}" "$host" "bash -s" <<REMOTE
set -u
$(declare -f merge_tower_mcp)
[ -x "\$HOME/.local/bin/tower" ] || { echo "NO_TOWER"; exit 10; }
conf="\$HOME/.config/opencode/opencode.jsonc"
if [ ! -e "\$conf" ] && [ -e "\$HOME/.config/opencode/opencode.json" ]; then conf="\$HOME/.config/opencode/opencode.json"; fi
mkdir -p "\$(dirname "\$conf")"
res=\$(merge_tower_mcp "\$conf"); rc=\$?
[ \$rc = 0 ] || { echo "MERGE_FAILED \$rc"; exit 11; }
echo "RESULT \$res"
unit=opencode.service; systemctl --user cat dots-opencode.service >/dev/null 2>&1 && unit=dots-opencode.service
if [ "\$res" = changed ]; then systemctl --user restart "\$unit" || { echo "RESTART_FAILED"; exit 12; }; fi
echo "ACTIVE \$(systemctl --user is-active "\$unit")"
REMOTE
)
rc=$?
set -e
case $rc in
  0) ;;
  10) die "tower is missing on the server: push it first with push.sh --tower" ;;
  11) die "the opencode config on the server is not plain JSON (a // comment?) or cannot be merged, left untouched: add \"mcp\": {\"tower\": {\"type\": \"local\", \"command\": [\"tower\", \"mcp\"], \"enabled\": true}} to it by hand" ;;
  12) die "the config was written but opencode.service (opencode.service or dots-opencode.service) did not restart" ;;
  *) die "the server step failed (exit $rc)" ;;
esac
res=$(printf '%s\n' "$out" | sed -n 's/^RESULT //p')
case $res in
  changed) log "tower MCP added, opencode restarted (the previous config is kept beside it as .bak-<time>)" ;;
  *) log "tower MCP already there, nothing changed" ;;
esac
log "opencode service: $(printf '%s\n' "$out" | sed -n 's/^ACTIVE //p')"
