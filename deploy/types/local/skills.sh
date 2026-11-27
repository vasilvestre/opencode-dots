#!/usr/bin/env bash
# Sends the team's shared skills and agents to a server as a snapshot pinned to a commit,
# and links them for opencode. The server holds no credential for the private repository:
# the workstation clone is the only place that has one. Run it on the workstation.
#
#   deploy/types/local/skills.sh dots@server --skills-repo LOCAL_CLONE [--commit REF] [--remote-dir DIR] [--dry-run]
#
# REF defaults to HEAD. DIR (default: skills, under the server's home) receives the
# snapshot; the previous one stays as DIR.prev. mcp.yaml and any config.yaml are never
# sent, the server gets a config of its own with two targets, and only `skillshare sync`
# and `skillshare sync agents` run there: never `sync mcp`, never `sync extras`.
# See docs/skills.md.
set -euo pipefail
cd "$(dirname "$0")/../../.."
# shellcheck source=../../lib.sh
. deploy/lib.sh

usage="usage: skills.sh user@host --skills-repo LOCAL_CLONE [--commit REF] [--remote-dir DIR] [--dry-run]"
[ $# -ge 1 ] || die "$usage"
host=$1
shift
repo= ref=HEAD remote=skills dry=0
while [ $# -gt 0 ]; do
  case "$1" in
    --skills-repo) repo=${2:?--skills-repo needs a directory}; shift ;;
    --commit) ref=${2:?--commit needs a ref}; shift ;;
    --remote-dir) remote=${2:?--remote-dir needs a directory}; shift ;;
    --dry-run) dry=1 ;;
    *) die "unknown argument: $1" ;;
  esac
  shift
done
[ -n "$repo" ] || die "$usage"
case "$remote" in /* | *..* | *"'"*) die "--remote-dir must be a plain directory name under the server's home" ;; esac

# (a) the clone and the commit
[ "$(git -C "$repo" rev-parse --is-inside-work-tree 2>/dev/null)" = true ] || die "$repo is not a git work tree"
[ -d "$repo/.skillshare" ] || die "$repo has no .skillshare directory"
commit=$(git -C "$repo" rev-parse --verify --quiet "$ref^{commit}") || die "cannot resolve $ref in $repo"
if [ -n "$(git -C "$repo" status --porcelain -- .skillshare)" ]; then
  die "uncommitted changes under .skillshare in $repo: commit or discard them, the snapshot must equal the commit"
fi

# (b) the binary: x86_64, static
bin=${SKILLSHARE_BIN:-$(command -v skillshare || true)}
[ -n "$bin" ] && [ -f "$bin" ] || die "skillshare is not installed here"
if [ "${DOTS_ELF_CHECK:-1}" != 0 ]; then
  [ "$(head -c4 "$bin" | od -An -c | tr -d ' ')" = '177ELF' ] || die "$bin is not an ELF binary"
  [ "$(od -An -tx1 -j18 -N1 "$bin" | tr -d ' ')" = 3e ] || die "$bin is not x86_64, the server is"
  if command -v ldd >/dev/null 2>&1 && ! ldd "$bin" 2>&1 | grep -qiE 'not a dynamic|statically linked'; then
    die "$bin is not statically linked"
  fi
fi
version=$("$bin" version 2>/dev/null | sed -n 's/\x1b\[[0-9;]*m//g;s/^.*\(v[0-9][0-9.]*\).*$/\1/p' | head -1)
log "skillshare ${version:-unknown version} from $bin"
log "snapshot of $repo at $commit"

ssh_opts=(-o BatchMode=yes)
excludes=(':(exclude,glob)**/mcp.yaml' ':(exclude,glob)**/config.yaml')

if [ "$dry" = 1 ]; then
  log "dry run, the server is not touched. It would:"
  log "  copy skillshare to $host:.local/bin/skillshare (temporary name, then rename)"
  log "  send these files to $host:$remote.new, then swap it into $host:$remote (previous kept as $remote.prev):"
  git -C "$repo" archive --format=tar "$commit" -- .skillshare "${excludes[@]}" | tar -t | grep -v '/$' | sed 's/^/    /'
  log "  write $remote/.snapshot and $remote/.skillshare/config.yaml (targets: \$HOME/.config/opencode/skills and .../agents, merge)"
  log "  run in $remote: skillshare sync -p, skillshare sync agents -p, skillshare status -p"
  exit 0
fi

log "copying the binary to $host"
ssh "${ssh_opts[@]}" "$host" "mkdir -p ~/.local/bin"
# A running binary cannot be overwritten, but it can be renamed over.
scp -q "${ssh_opts[@]}" "$bin" "$host:.local/bin/skillshare.new"
ssh "${ssh_opts[@]}" "$host" "chmod 755 ~/.local/bin/skillshare.new && mv -f ~/.local/bin/skillshare.new ~/.local/bin/skillshare"

# (c) the snapshot, unpacked beside the live one
log "sending the snapshot to $host:$remote"
ssh "${ssh_opts[@]}" "$host" "rm -rf '$remote.new' && mkdir -p '$remote.new'"
git -C "$repo" archive --format=tar "$commit" -- .skillshare "${excludes[@]}" |
  ssh "${ssh_opts[@]}" "$host" "tar -x -C '$remote.new'"

# (c, d, e) mark it, configure it, swap it in, link it. The old snapshot stays until the swap worked.
ssh "${ssh_opts[@]}" "$host" "bash -s" <<REMOTE
set -euo pipefail
cd "\$HOME"
export PATH="\$HOME/.local/bin:\$PATH"
printf '%s\n%s\n' '$commit' "\$(date -u +%Y-%m-%dT%H:%M:%SZ)" >'$remote.new/.snapshot'
# Only the two targets of opencode. No MCP, no ~/.claude, no universal target.
cat >'$remote.new/.skillshare/config.yaml' <<CONF
targets:
  - name: opencode
    skills:
      path: \$HOME/.config/opencode/skills
      mode: merge
    agents:
      path: \$HOME/.config/opencode/agents
      mode: merge
CONF
mkdir -p "\$HOME/.config/opencode/skills" "\$HOME/.config/opencode/agents"
if [ -d '$remote' ]; then
  rm -rf '$remote.prev'
  mv '$remote' '$remote.prev'
fi
mv '$remote.new' '$remote' || { [ -d '$remote.prev' ] && mv '$remote.prev' '$remote'; exit 1; }
cd '$remote'
skillshare sync -p
skillshare sync agents -p
skillshare status -p
REMOTE
log "done: $commit is live in $host:$remote (previous snapshot in $host:$remote.prev)"
