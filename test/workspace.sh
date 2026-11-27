#!/usr/bin/env bash
# Tests bin/workspace with a local bare repository standing in for the server, one writer and
# two readers. Nothing leaves the machine.
#   test/workspace.sh
set -uo pipefail
cd "$(dirname "$0")/.."
ws=$PWD/bin/workspace
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
# HEAD on master while the writer pushes main: what a bare repository made with git's default gives.
git init -q --bare -b master "$tmp/remote.git"
export DOTS_WORKSPACE_REMOTE=$tmp/remote.git

fail=0
check() { # check <description> <command...>
  local d=$1
  shift
  if "$@" >/dev/null 2>&1; then echo "ok    $d"; else echo "FAIL  $d"; fail=1; fi
}
refuses() { # refuses <description> <command...>: the command must fail
  local d=$1
  shift
  if "$@" >/dev/null 2>&1; then echo "FAIL  $d (it succeeded)"; fail=1; else echo "ok    $d"; fi
}
as() { # as <name> <role> <command...>
  local name=$1 role=$2
  shift 2
  env STATE_DIR="$tmp/$name/state" DOTS_WORKSPACE_DIR="$tmp/$name/ws" DOTS_WORKSPACE_ROLE=$role DOTS_ACTOR=$name DOTS_HOST=$name "$ws" "$@"
}

# The writer (the server) has learned things, and holds files that must never leave it.
w=$tmp/server/state/tend/acme/app
mkdir -p "$w/work" "$tmp/server/state/tend/acme/lib"
printf '# lessons\n- rule one\n' >"$w/lessons.md"
printf 'memory\n' >"$w/memory.md"
printf 'raised\n' >"$w/raised.txt"
printf 'TOKEN=secret\n' >"$w/.env"
printf 'a clone\n' >"$w/work/file.txt"
printf '{"x":1}\n' >"$w/prs.json"
printf 'since\n' >"$w/since"
printf '# lib lessons\n' >"$tmp/server/state/tend/acme/lib/lessons.md"

# The worklog of one issue and one pull request on acme/app, one pull request on acme/lib.
mkdir -p "$tmp/server/state/fix/acme/app/issue-7" "$tmp/server/state/replies/pr9" "$tmp/server/state/replies/pr10"
printf '# worklog issue 7\n' >"$tmp/server/state/fix/acme/app/issue-7/worklog.md"
printf '{"url":"https://github.com/acme/app/pull/9"}\n' >"$tmp/server/state/replies/pr9/pr.json"
printf '# worklog pr 9\n' >"$tmp/server/state/replies/pr9/worklog.md"
printf '{"url":"https://github.com/acme/lib/pull/10"}\n' >"$tmp/server/state/replies/pr10/pr.json"
printf '# worklog pr 10\n' >"$tmp/server/state/replies/pr10/worklog.md"

echo "== the writer"
check "init creates the first commit on an empty remote" as server writer init
# A server whose state was just cleared has nothing learnt yet: publishing must still work, not die
# on a missing tend/ (it made the tick task workspace-sync fail every 15 minutes).
mv "$tmp/server/state/tend" "$tmp/server/state/tend.held"
mv "$tmp/server/state/fix" "$tmp/server/state/fix.held"; mv "$tmp/server/state/replies" "$tmp/server/state/replies.held"
check "publish with nothing learnt yet succeeds" as server writer publish
mv "$tmp/server/state/tend.held" "$tmp/server/state/tend"
mv "$tmp/server/state/fix.held" "$tmp/server/state/fix"; mv "$tmp/server/state/replies.held" "$tmp/server/state/replies"
check "publish succeeds" as server writer publish
sw=$tmp/server/ws
check "lessons are in the repository" test -f "$sw/tend/acme/app/lessons.md"
check "memory and raised are in the repository" test -f "$sw/tend/acme/app/memory.md" -a -f "$sw/tend/acme/app/raised.txt"
refuses "a secret is never copied" test -e "$sw/tend/acme/app/.env"
refuses "a clone is never copied" test -e "$sw/tend/acme/app/work"
refuses "logs and json are never copied" test -e "$sw/tend/acme/app/prs.json"
refuses "the cursor is never copied" test -e "$sw/tend/acme/app/since"
check "the issue worklog is in the repository" test -f "$sw/tend/acme/app/worklogs/issue-7.md"
check "the pull request worklog follows its repository" test -f "$sw/tend/acme/app/worklogs/pr9.md" -a -f "$sw/tend/acme/lib/worklogs/pr10.md"
refuses "another repository's worklog does not land here" test -e "$sw/tend/acme/app/worklogs/pr10.md"
check "publishing again says nothing new" as server writer publish
refuses "a reader cannot publish" as ada reader publish

echo "== the readers"
check "reader A inits" as ada reader init
check "reader B inits" as grace reader init
check "install puts the lessons in A's state" as ada reader install
check "the lessons are the writer's" diff "$w/lessons.md" "$tmp/ada/state/tend/acme/app/lessons.md"
check "the second repository comes too" test -f "$tmp/ada/state/tend/acme/lib/lessons.md"
check "the worklogs are installed too" test -f "$tmp/ada/state/tend/acme/app/worklogs/issue-7.md" -a -f "$tmp/ada/state/tend/acme/app/worklogs/pr9.md"
refuses "the writer cannot install over its own lessons" as server writer install
refuses "a reader cannot overwrite through publish" as grace reader publish

echo "== inbox"
check "A notes something" as ada reader note acme/app wi_123 "the retry should not mask a 429"
check "A notes again, same file" as ada reader note acme/app wi_123 "second thought"
check "B notes something" as grace reader note acme/lib github:acme/lib#4 "tests need a fixture"
refuses "a repository name is checked" as ada reader note "not a repo" wi_1 text
refuses "a subject is one line" as ada reader note acme/app $'two\nlines' text
check "A releases" as ada reader release
check "B releases without a conflict" as grace reader release
check "releasing nothing is fine" as ada reader release
check "each machine has its own inbox file" test "$(ls "$tmp/ada/ws/inbox" | wc -l)" -ge 1
refuses "a reader cannot read the writer's inbox command" as ada reader inbox

echo "== the writer folds the inbox"
out=$(as server writer inbox acme/app)
check "the inbox shows A's notes on the repository" grep -q "should not mask a 429" <<<"$out"
check "both of A's entries are there" test "$(grep -c '^## ' <<<"$out")" = 2
refuses "B's note on another repository is not shown" grep -q "fixture" <<<"$out"
all=$(as server writer inbox)
check "the whole inbox has B's note" grep -q "fixture" <<<"$all"
check "archive moves the files aside" as server writer archive
refuses "nothing is left in the live inbox" test -n "$(ls "$sw/inbox"/*.md 2>/dev/null)"
check "archive again has nothing to do" as server writer archive

echo "== a lesson reaches a reader at the next sync"
printf '# lessons\n- rule one\n- rule two\n' >"$w/lessons.md"
as server writer sync >/dev/null
as ada reader sync >/dev/null
check "A sees rule two" grep -q "rule two" "$tmp/ada/state/tend/acme/app/lessons.md"
check "checkout shows where the lessons are" as grace reader checkout acme/app

if [ "$fail" = 0 ]; then echo "workspace test: all checks passed"; else echo "workspace test: failures above" >&2; exit 1; fi
