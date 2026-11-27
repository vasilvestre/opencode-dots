#!/usr/bin/env bash
# bin/backup-server keeps the lessons, repos.yaml and the workspace repository, and leaves out
# clones, backups of config and secrets. A fake ssh runs the remote command in a temporary home.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
home=$work/home
mkdir -p "$home/dots/state/tend/o/r" "$home/dots/state/replies/pr1/work" "$home/workspaces/default.git" "$work/bin" "$work/out"
echo lesson >"$home/dots/state/tend/o/r/lessons.md"
echo repos >"$home/dots/repos.yaml"
echo stale >"$home/dots/repos.yaml.bak"
echo SECRET=1 >"$home/dots/.env"
echo clone >"$home/dots/state/replies/pr1/work/file"
git init -q --bare "$home/workspaces/default.git"
cat >"$work/bin/ssh" <<'EOF2'
#!/usr/bin/env bash
while [ $# -gt 0 ] && [ "$1" = -o ]; do shift 2; done
shift
HOME=$FAKE_HOME bash -c "$*"
EOF2
chmod +x "$work/bin/ssh"
export FAKE_HOME=$home PATH="$work/bin:$PATH"
fail=0
ok() { echo "ok    $1"; }
ko() { echo "FAIL  $1"; fail=1; }
"$root/bin/backup-server" dots@fake "$work/out" >/dev/null
a=$(ls "$work/out"/dots-*.tgz)
list=$(tar -tzf "$a")
echo "$list" | grep -q 'dots/state/tend/o/r/lessons.md' && ok "the lessons are in the archive" || ko "the lessons are in the archive"
echo "$list" | grep -q 'dots/repos.yaml$' && ok "repos.yaml is in the archive" || ko "repos.yaml is in the archive"
echo "$list" | grep -q 'workspaces/default.git/HEAD' && ok "the workspace repository is in the archive" || ko "the workspace repository is in the archive"
echo "$list" | grep -q '\.env' && ko "a secret file is left out" || ok "a secret file is left out"
echo "$list" | grep -q '/work/' && ko "a clone is left out" || ok "a clone is left out"
echo "$list" | grep -q '\.bak' && ko "a .bak copy is left out" || ok "a .bak copy is left out"
rm -rf "$home/workspaces" "$work/out"/*
"$root/bin/backup-server" dots@fake "$work/out" >/dev/null
tar -tzf "$work/out"/dots-*.tgz | grep -q 'dots/repos.yaml$' && ok "a server without a workspace still backs up" || ko "a server without a workspace still backs up"
[ "$fail" = 0 ] && echo "backup test: all checks passed"
exit "$fail"
