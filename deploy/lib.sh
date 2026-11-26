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

# The machine's architecture as the release archives of awf name it.
awf_arch() {
  case "$(uname -m)" in
    x86_64) echo amd64 ;;
    aarch64 | arm64) echo arm64 ;;
    *) die "unsupported architecture: $(uname -m)" ;;
  esac
}
