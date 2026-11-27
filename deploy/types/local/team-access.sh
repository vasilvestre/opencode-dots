#!/usr/bin/env bash
# Manages the SSH keys of the developers who reach the dashboard through a port forward
# (docs/team-ui.md). It edits ~/.ssh/authorized_keys of the user the dots run as, so it needs
# no root. Run it on the workstation of the admin.
#
#   deploy/types/local/team-access.sh user@host add NAME KEYFILE [--port 8098] [--dry-run]
#   deploy/types/local/team-access.sh user@host remove NAME
#   deploy/types/local/team-access.sh user@host list
#
# Each key becomes one line that can open the dashboard tunnel and nothing else:
#
#   restrict,port-forwarding,permitopen="127.0.0.1:PORT",command="/bin/false" <type> <key> dots-team:NAME
#
# NAME is [a-z0-9][a-z0-9-]{0,30}. KEYFILE holds one PUBLIC key with no options. Adding a name
# twice replaces its line. Only lines tagged dots-team:NAME are ever touched; every other line
# stays as it is. A changed file is written through a temporary file in the same directory
# and renamed in, after a copy kept beside it as authorized_keys.bak-<time>.
set -euo pipefail
cd "$(dirname "$0")/../../.."
# shellcheck source=../../lib.sh
. deploy/lib.sh

usage() { die "usage: team-access.sh user@host add NAME KEYFILE [--port 8098] [--dry-run] | remove NAME | list"; }

[ $# -ge 2 ] || usage
host=$1 cmd=$2
shift 2

name_ok() { [[ $1 =~ ^[a-z0-9][a-z0-9-]{0,30}$ ]]; }

# The key line pieces: refuses a private key, a file with several keys, a key with options.
read_pubkey() { # read_pubkey <file>: prints "<type> <base64>"
  local f=$1 lines
  [ -f "$f" ] && [ -r "$f" ] || die "cannot read $f"
  if grep -q -- '-----BEGIN' "$f"; then die "$f looks like a private key: give the public one (.pub)"; fi
  lines=$(grep -v -e '^[[:space:]]*$' -e '^[[:space:]]*#' "$f" || true)
  [ -n "$lines" ] || die "$f holds no key"
  [ "$(printf '%s\n' "$lines" | wc -l)" = 1 ] || die "$f holds several keys: one key per developer"
  local ktype kbody
  read -r ktype kbody _ <<<"$lines"
  if ! [[ $ktype =~ ^(ssh-(ed25519|rsa|dss)|ecdsa-sha2-nistp(256|384|521)|sk-ssh-ed25519@openssh\.com|sk-ecdsa-sha2-nistp256@openssh\.com)$ ]]; then
    die "$f does not start with a key type: a key that carries options is refused, give the bare public key"
  fi
  [[ ${kbody:-} =~ ^[A-Za-z0-9+/]+=*$ ]] || die "$f: the key body is not base64"
  ssh-keygen -l -f "$f" >/dev/null 2>&1 || die "ssh-keygen does not accept $f as a public key"
  printf '%s %s\n' "$ktype" "$kbody"
}

# Runs a script on the server with the given NAME and PORT; the values are validated here and
# contain only [a-z0-9-] and digits.
remote() { # remote <op> <name> <port> <key line, or empty>
  ssh -o BatchMode=yes "$host" "bash -s" <<REMOTE
set -eu
op='$1' name='$2' port='$3' line='$4'
umask 077
d="\$HOME/.ssh"
f="\$d/authorized_keys"
mkdir -p "\$d"
chmod 700 "\$d"
[ -e "\$f" ] || : >"\$f"
chmod 600 "\$f"
tag="dots-team:\$name"
case \$op in
  list)
    awk '\$NF ~ /^dots-team:/ { print \$NF, \$0 }' "\$f" | while read -r tg rest; do
      n=\${tg#dots-team:}
      p=\$(printf '%s\n' "\$rest" | sed -n 's/.*permitopen="[^":]*:\([0-9]*\)".*/\1/p')
      fp=\$(printf '%s\n' "\$rest" | awk '{ print \$2, \$3 }' | ssh-keygen -l -f /dev/stdin 2>/dev/null | awk '{ print \$2 }')
      printf '%s port=%s %s\n' "\$n" "\${p:-?}" "\${fp:-unreadable}"
    done
    exit 0 ;;
esac
tmp=\$(mktemp "\$d/authorized_keys.XXXXXX")
trap 'rm -f "\$tmp"' EXIT
awk -v t="\$tag" '\$NF != t' "\$f" >"\$tmp"
had=0
[ "\$(awk -v t="\$tag" '\$NF == t' "\$f" | wc -l)" = 0 ] || had=1
if [ "\$op" = add ]; then printf '%s\n' "\$line" >>"\$tmp"; fi
if cmp -s "\$tmp" "\$f"; then echo unchanged; exit 0; fi
cp -p "\$f" "\$f.bak-\$(date +%Y%m%d%H%M%S)"
chmod 600 "\$tmp"
mv -f "\$tmp" "\$f"
trap - EXIT
if [ "\$op" = add ]; then
  if [ \$had = 1 ]; then echo replaced; else echo added; fi
else
  echo removed
fi
REMOTE
}

case $cmd in
  add)
    [ $# -ge 2 ] || usage
    name=$1 keyfile=$2
    shift 2
    port=8098 dry=0
    while [ $# -gt 0 ]; do
      case $1 in
        --port) port=${2:?--port needs a number}; shift ;;
        --dry-run) dry=1 ;;
        *) die "unknown argument: $1" ;;
      esac
      shift
    done
    name_ok "$name" || die "NAME must match [a-z0-9][a-z0-9-]{0,30}"
    [[ $port =~ ^[0-9]{1,5}$ ]] && [ "$port" -ge 1 ] && [ "$port" -le 65535 ] || die "--port must be 1 to 65535"
    key=$(read_pubkey "$keyfile")
    line="restrict,port-forwarding,permitopen=\"127.0.0.1:$port\",command=\"/bin/false\" $key dots-team:$name"
    if [ $dry = 1 ]; then
      printf '%s\n' "$line"
      exit 0
    fi
    res=$(remote add "$name" "$port" "$line")
    log "$name: $res on $host (port $port)"
    ;;
  remove)
    [ $# -eq 1 ] || usage
    name_ok "$1" || die "NAME must match [a-z0-9][a-z0-9-]{0,30}"
    res=$(remote remove "$1" 0 "")
    case $res in
      removed) log "$1: removed on $host" ;;
      *) log "$1: no such key on $host, nothing changed" ;;
    esac
    ;;
  list)
    [ $# -eq 0 ] || usage
    remote list x 0 ""
    ;;
  *) usage ;;
esac
