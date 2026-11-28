#!/usr/bin/env bash
# Pushes what a small server does not build (ADR 0028): bin/repos and bin/dashboard,
# compiled here, and with --tower the tower binary and its agent_memory extension, which
# have no Linux release archive. Run it on the workstation, from a checkout.
#
#   deploy/types/local/push.sh dots@server [--tower] [--repos-yaml FILE] [--remote-dir DIR]
#
# --repos-yaml copies a repos.yaml (private, mode 600) and runs `bin/repos generate`
# there, so the generated files come from it and are not edited by hand.
set -euo pipefail
cd "$(dirname "$0")/../../.."
# shellcheck source=../../lib.sh
. deploy/lib.sh

[ $# -ge 1 ] || die "usage: push.sh user@host [--tower] [--repos-yaml FILE] [--remote-dir DIR]"
host=$1
shift
tower=0 repos_yaml= remote=dots
while [ $# -gt 0 ]; do
  case "$1" in
    --tower) tower=1 ;;
    --repos-yaml) repos_yaml=${2:?--repos-yaml needs a file}; shift ;;
    --remote-dir) remote=${2:?--remote-dir needs a directory}; shift ;;
    *) die "unknown argument: $1" ;;
  esac
  shift
done

out=$(mktemp -d)
trap 'rm -rf "$out"' EXIT
log "building bin/repos and bin/dashboard here"
CGO_ENABLED=0 go build -o "$out/repos" ./cmd/repos
go build -o "$out/dashboard" ./cmd/dashboard
CGO_ENABLED=0 go build -o "$out/health" ./cmd/health

ssh_opts=(-o BatchMode=yes)
# A binary that is running cannot be overwritten ("text file busy"), but it can be
# renamed over: copy under a temporary name, then move it into place.
send() { # send <local file> <remote path relative to the home directory>
  scp -q "${ssh_opts[@]}" "$1" "$host:$2.new"
  ssh "${ssh_opts[@]}" "$host" "chmod 755 '$2.new' && mv -f '$2.new' '$2'"
}

log "copying to $host:$remote"
ssh "${ssh_opts[@]}" "$host" "mkdir -p '$remote/bin'"
send "$out/repos" "$remote/bin/repos"
send "$out/dashboard" "$remote/bin/dashboard"
send "$out/health" "$remote/bin/health"

if [ "$tower" = 1 ]; then
  command -v tower >/dev/null 2>&1 || die "tower is not installed here, build it first (see the README)"
  ext=${TOWER_EXTENSIONS_DIR:-$HOME/.local/share/tower/extensions}
  [ -d "$ext/agent_memory" ] || die "no agent_memory extension in $ext"
  log "tower and its agent_memory extension"
  ssh "${ssh_opts[@]}" "$host" "mkdir -p ~/.local/bin ~/.local/share/tower/extensions"
  send "$(command -v tower)" ".local/bin/tower"
  scp -q -r "${ssh_opts[@]}" "$ext/agent_memory" "$host:.local/share/tower/extensions/"
fi

if [ -n "$repos_yaml" ]; then
  [ -f "$repos_yaml" ] || die "$repos_yaml not found"
  log "repos.yaml and the generated files"
  scp -q "${ssh_opts[@]}" "$repos_yaml" "$host:$remote/repos.yaml"
  ssh "${ssh_opts[@]}" "$host" "chmod 600 '$remote/repos.yaml' && cd '$remote' && bin/repos generate -state-path \"\$PWD/state/awf-gh/seen.jsonl\""
fi
log "done"
