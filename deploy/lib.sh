# Helpers shared by the deploy scripts. Source it; it sets no options of its own.

log() { printf 'deploy: %s\n' "$*"; }
warn() { printf 'deploy: warning: %s\n' "$*" >&2; }
die() { printf 'deploy: %s\n' "$*" >&2; exit 1; }

# True when a systemd user manager is reachable. In a container or a plain chroot it
# is not, and the scripts then write the files and skip the enabling.
have_systemd_user() {
  [ -d /run/systemd/system ] && command -v systemctl >/dev/null 2>&1 &&
    systemctl --user show-environment >/dev/null 2>&1
}

# Merges the tower MCP entry into an opencode config (plain JSON) and touches no other
# key. It is shared by mcp.sh, which ships it to the server with `declare -f`, and by
# user.sh, so the two cannot drift. It needs only bash and jq, and prints "changed" or
# "unchanged". Returns 2 when the file does not parse as plain JSON (a JSONC file with
# comments, say: it is never guessed at), 3 when jq cannot merge. A changed file keeps
# the previous one as <file>.bak-<timestamp>; the new one is validated, then renamed in.
merge_tower_mcp() { # merge_tower_mcp <config file>
  local f=$1 tmp bak want='{"type":"local","command":["tower","mcp"],"enabled":true}'
  if [ -e "$f" ]; then
    jq -e . "$f" >/dev/null 2>&1 || return 2
    if [ "$(jq -c --argjson w "$want" '.mcp.tower == $w' "$f" 2>/dev/null)" = true ]; then
      echo unchanged
      return 0
    fi
  fi
  tmp=$(mktemp "$f.XXXXXX") || return 3
  if [ -e "$f" ]; then
    jq --argjson w "$want" '.mcp.tower = $w' "$f" >"$tmp" 2>/dev/null
  else
    jq -n --argjson w "$want" '{mcp: {tower: $w}}' >"$tmp" 2>/dev/null
  fi || { rm -f "$tmp"; return 3; }
  jq -e '.mcp.tower.command[0] == "tower"' "$tmp" >/dev/null 2>&1 || { rm -f "$tmp"; return 3; }
  if [ -e "$f" ]; then
    bak="$f.bak-$(date +%Y%m%d%H%M%S)"
    cp -p "$f" "$bak"
    chmod --reference="$f" "$tmp" 2>/dev/null || true
  fi
  mv -f "$tmp" "$f"
  echo changed
}

# The machine's architecture as the release archives of awf name it.
awf_arch() {
  case "$(uname -m)" in
    x86_64) echo amd64 ;;
    aarch64 | arm64) echo arm64 ;;
    *) die "unsupported architecture: $(uname -m)" ;;
  esac
}
