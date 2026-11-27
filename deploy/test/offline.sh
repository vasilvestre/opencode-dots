#!/usr/bin/env bash
# Offline tests of mcp.sh, skills.sh and the opencode config user.sh writes. No docker, no
# network, no server: a fake ssh and scp on PATH run the commands against a temporary
# directory that stands for the server's home, and fake skillshare and systemctl log what
# they are asked.
#
#   deploy/test/offline.sh
set -euo pipefail
root=$(cd "$(dirname "$0")/../.." && pwd)
cd "$root"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
srv=$work/srv
mkdir -p "$srv" "$work/bin" "$work/ws"

fail=0
check() { # check <description> <command...>
  local d=$1
  shift
  if "$@" >/dev/null 2>&1; then echo "ok    $d"; else echo "FAIL  $d"; fail=1; fi
}
refuses() { # refuses <description> <command...>: it must exit non-zero
  local d=$1
  shift
  if "$@" >/dev/null 2>&1; then echo "FAIL  $d (it succeeded)"; fail=1; else echo "ok    $d"; fi
}

# The fakes. "The server" is $srv, its home.
cat >"$work/bin/ssh" <<'EOF'
#!/usr/bin/env bash
while [ $# -gt 0 ] && [ "$1" = -o ]; do shift 2; done
shift # the host
cd "$FAKE_SRV" && HOME=$FAKE_SRV bash -c "$*"
EOF
cat >"$work/bin/scp" <<'EOF'
#!/usr/bin/env bash
while [ $# -gt 0 ]; do
  case "$1" in -q) shift ;; -o) shift 2 ;; *) break ;; esac
done
cp "$1" "$FAKE_SRV/${2#*:}"
EOF
cat >"$work/bin/systemctl" <<'EOF'
#!/usr/bin/env bash
echo "$*" >>"$HOME/systemctl.log"
[ "$2" = is-active ] && echo active
exit 0
EOF
cat >"$work/skillshare" <<'EOF'
#!/usr/bin/env bash
if [ "$1" = version ]; then echo "skillshare v0.30.13"; exit 0; fi
echo "$PWD: $*" >>"$HOME/skillshare.log"
echo "fake skillshare $*"
EOF
chmod +x "$work/bin/"* "$work/skillshare"
export FAKE_SRV=$srv PATH="$work/bin:$PATH" SKILLSHARE_BIN=$work/skillshare DOTS_ELF_CHECK=0
user=dots@fake

# ---- mcp.sh
conf=$srv/.config/opencode/opencode.jsonc
mkdir -p "$srv/.config/opencode"
refuses "mcp.sh refuses when tower is missing" deploy/types/local/mcp.sh "$user"
mkdir -p "$srv/.local/bin"
printf '#!/bin/sh\n' >"$srv/.local/bin/tower"
chmod +x "$srv/.local/bin/tower"

printf '// comment\n{ "a": 1 }\n' >"$conf"
cp "$conf" "$work/jsonc.orig"
refuses "mcp.sh refuses a config with comments" deploy/types/local/mcp.sh "$user"
check "the commented config is untouched" cmp "$conf" "$work/jsonc.orig"
check "nothing was restarted" test ! -e "$srv/systemctl.log"

printf '{"$schema":"x","experimental":{"location_idle_timeout":"5 minutes"},"mcp":{"other":{"type":"remote","url":"u"}},"model":"m"}\n' >"$conf"
out=$(deploy/types/local/mcp.sh "$user")
check "mcp.sh adds tower" jq -e '.mcp.tower == {type:"local",command:["tower","mcp"],enabled:true}' "$conf"
check "mcp.sh keeps every other key" jq -e '.model == "m" and .mcp.other.url == "u" and .experimental.location_idle_timeout == "5 minutes" and .["$schema"] == "x"' "$conf"
check "mcp.sh keeps a backup" test "$(find "$srv/.config/opencode" -name 'opencode.jsonc.bak-*' | wc -l)" = 1
check "the backup is the old file" jq -e 'has("mcp") and (.mcp | has("tower") | not)' "$(find "$srv/.config/opencode" -name 'opencode.jsonc.bak-*')"
check "opencode was restarted" grep -q -- '--user restart opencode.service' "$srv/systemctl.log"
check "it prints is-active" grep -q 'opencode.service: active' <<<"$out"
check "no temporary file is left" test "$(find "$srv/.config/opencode" -name 'opencode.jsonc.??????' | wc -l)" = 0
cp "$conf" "$work/after1"
: >"$srv/systemctl.log"
out=$(deploy/types/local/mcp.sh "$user")
check "a second run changes nothing" cmp "$conf" "$work/after1"
check "a second run says so" grep -q 'nothing changed' <<<"$out"
check "a second run adds no backup" test "$(find "$srv/.config/opencode" -name 'opencode.jsonc.bak-*' | wc -l)" = 1
check "no password or service.json in the output" test -z "$(grep -i 'password\|service.json' <<<"$out")"

rm -f "$conf" "$srv/.config/opencode/"*.bak-* "$srv/systemctl.log"
deploy/types/local/mcp.sh "$user" >/dev/null
check "mcp.sh creates a missing config" jq -e '.mcp.tower.command == ["tower","mcp"]' "$conf"

# ---- user.sh: the config it writes
cat >"$work/bin/id" <<'EOF'
#!/usr/bin/env bash
[ "$1" = -u ] && { echo 1000; exit 0; }
exec /usr/bin/id "$@"
EOF
chmod +x "$work/bin/id"
# Run only opencode_config: source user.sh's function without running its steps.
extract() { sed -n '/^opencode_config() {/,/^}/p' deploy/types/local/user.sh; }
run_config() { # run_config <home> <tower yes|no>
  mkdir -p "$1/.local/bin"
  if [ "$2" = yes ]; then printf '#!/bin/sh\n' >"$1/.local/bin/tower"; chmod +x "$1/.local/bin/tower"; fi
  HOME=$1 bash -c ". deploy/lib.sh; $(extract); opencode_config" >/dev/null 2>&1
}
run_config "$work/h-no" no
printf '{\n  "$schema": "https://opencode.ai/config.json",\n  "experimental": { "location_idle_timeout": "5 minutes" }\n}\n' >"$work/expected"
check "user.sh without tower writes today's bytes" cmp "$work/h-no/.config/opencode/opencode.jsonc" "$work/expected"
run_config "$work/h-yes" yes
check "user.sh with tower adds the entry" jq -e '.mcp.tower.command == ["tower","mcp"] and .experimental.location_idle_timeout == "5 minutes" and .["$schema"] != null' "$work/h-yes/.config/opencode/opencode.jsonc"

# ---- skills.sh
repo=$work/ws/skills-repo
mkdir -p "$repo/.skillshare/skills/team__a" "$repo/.skillshare/agents" "$repo/.skillshare/extras/rules"
echo '---' >"$repo/.skillshare/skills/team__a/SKILL.md"
echo agent >"$repo/.skillshare/agents/team-x.md"
echo rule >"$repo/.skillshare/extras/rules/r.md"
echo 'servers: {}' >"$repo/mcp.yaml"
echo 'servers: {}' >"$repo/.skillshare/mcp.yaml"
echo '.skillshare/config.yaml' >"$repo/.gitignore"
echo 'targets: []' >"$repo/.skillshare/config.yaml"
git -C "$repo" init -q
git -C "$repo" -c user.name=t -c user.email=t@t add -A
git -C "$repo" -c user.name=t -c user.email=t@t commit -q -m one
c1=$(git -C "$repo" rev-parse HEAD)
rm -rf "$srv/.config/opencode/skills" "$srv/skillshare.log"

refuses "skills.sh refuses a directory that is not a git work tree" deploy/types/local/skills.sh "$user" --skills-repo "$work/ws"
mkdir -p "$work/ws/plain"
git -C "$work/ws/plain" init -q
refuses "skills.sh refuses a repository without .skillshare" deploy/types/local/skills.sh "$user" --skills-repo "$work/ws/plain"
refuses "skills.sh refuses an unknown commit" deploy/types/local/skills.sh "$user" --skills-repo "$repo" --commit nope
echo dirty >>"$repo/.skillshare/agents/team-x.md"
refuses "skills.sh refuses uncommitted changes under .skillshare" deploy/types/local/skills.sh "$user" --skills-repo "$repo"
git -C "$repo" checkout -q -- .skillshare/agents/team-x.md
check "nothing was sent by the refusals" test ! -e "$srv/skills"
printf 'not elf\n' >"$work/notelf"
refuses "skills.sh refuses a binary that is not an ELF" env DOTS_ELF_CHECK=1 SKILLSHARE_BIN="$work/notelf" deploy/types/local/skills.sh "$user" --skills-repo "$repo"

out=$(deploy/types/local/skills.sh "$user" --skills-repo "$repo" --dry-run)
check "--dry-run touches nothing on the server" test -z "$(find "$srv" -maxdepth 1 -name 'skills*' -o -maxdepth 1 -name 'skillshare.log')"
check "--dry-run lists the files" grep -q 'team__a/SKILL.md' <<<"$out"
check "--dry-run does not list mcp.yaml" test -z "$(grep 'mcp.yaml' <<<"$out" | grep -v '^deploy: ')"
check "--dry-run did not copy the binary" test ! -e "$srv/.local/bin/skillshare"

out=$(deploy/types/local/skills.sh "$user" --skills-repo "$repo")
s=$srv/skills
check "the binary is on the server" test -x "$srv/.local/bin/skillshare"
check "no temporary binary left" test ! -e "$srv/.local/bin/skillshare.new"
check "the skill is sent" test -f "$s/.skillshare/skills/team__a/SKILL.md"
check "the agent is sent" test -f "$s/.skillshare/agents/team-x.md"
check "mcp.yaml is not sent" test -z "$(find "$s" -name mcp.yaml)"
check "the clone's config.yaml is not sent" test "$(grep -c 'targets: \[\]' "$s/.skillshare/config.yaml")" = 0
check ".snapshot holds the commit" grep -qx "$c1" "$s/.snapshot"
check ".snapshot holds a date" grep -Eq '^[0-9]{4}-[0-9]{2}-[0-9]{2}T' "$s/.snapshot"
want="targets:
  - name: opencode
    skills:
      path: $srv/.config/opencode/skills
      mode: merge
    agents:
      path: $srv/.config/opencode/agents
      mode: merge"
check "the config has the two opencode targets and nothing else" test "$(cat "$s/.skillshare/config.yaml")" = "$want"
check "the config names no mcp, no claude, no universal" test -z "$(grep -iE 'mcp|claude|universal|\.agents' "$s/.skillshare/config.yaml")"
check "only the two syncs and status ran" test "$(sed 's/^[^:]*: //' "$srv/skillshare.log" | paste -sd'|')" = "sync -p|sync agents -p|status -p"
check "they ran inside the snapshot" test "$(cut -d: -f1 "$srv/skillshare.log" | sort -u)" = "$s"
check "no sync mcp or extras" test -z "$(grep -E 'mcp|extras' "$srv/skillshare.log")"
check "the status is printed" grep -q 'fake skillshare status -p' <<<"$out"
check "no previous snapshot on a first run" test ! -e "$srv/skills.prev"

# a second commit, then twice the same
echo two >>"$repo/.skillshare/agents/team-x.md"
git -C "$repo" -c user.name=t -c user.email=t@t commit -q -am two
c2=$(git -C "$repo" rev-parse HEAD)
deploy/types/local/skills.sh "$user" --skills-repo "$repo" >/dev/null
check "the new commit is live" grep -qx "$c2" "$s/.snapshot"
check "the previous snapshot is kept" grep -qx "$c1" "$srv/skills.prev/.snapshot"
deploy/types/local/skills.sh "$user" --skills-repo "$repo" >/dev/null
check "a second run with the same commit works" grep -qx "$c2" "$s/.snapshot"
check "a pinned older commit works" deploy/types/local/skills.sh "$user" --skills-repo "$repo" --commit "$c1"
check "the older commit is live" grep -qx "$c1" "$s/.snapshot"
check "no temporary snapshot is left" test ! -e "$srv/skills.new"

if [ "$fail" = 0 ]; then echo "deploy offline test: all checks passed"; else echo "deploy offline test: failures above" >&2; exit 1; fi
