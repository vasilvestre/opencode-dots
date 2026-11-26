#!/usr/bin/env bash
# Tests bin/held against a stub. The exit code is the whole contract: a workflow step
# reads 0 as free and 3 as a human claim (ADR 0026).
set -uo pipefail
cd "$(dirname "$0")/.."
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
fail=0
check() { # check <description> <command...>
  local d=$1
  shift
  if "$@" >/dev/null 2>&1; then echo "ok    $d"; else echo "FAIL  $d"; fail=1; fi
}

code() { # code <repos exit status>: what bin/held answers for that stub
  printf '#!/bin/sh\necho "held by claim cl_test"\nexit %s\n' "$1" >"$tmp/repos"
  chmod +x "$tmp/repos"
  DOTS_REPOS=$tmp/repos bin/held acme/app fix-login >/dev/null 2>&1
  echo $?
}

check "a free branch answers 0" test "$(code 0)" = 0
check "a human claim answers 3" test "$(code 10)" = 3
check "a broken check answers 1" test "$(code 1)" = 1
check "two arguments are required" test "$(bin/held acme/app >/dev/null 2>&1; echo $?)" = 2
check "no repos binary answers 1" \
  test "$(env PATH=/usr/bin:/bin DOTS_REPOS=/nonexistent bin/held acme/app fix-login >/dev/null 2>&1; echo $?)" = 1

if [ "$fail" = 0 ]; then echo "held test: all checks passed"; else echo "held test: failures above" >&2; exit 1; fi
