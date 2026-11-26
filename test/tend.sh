#!/usr/bin/env bash
# Tests what bin/tend posts to awf serve for each tend.list line. A line with only
# `repo auto_merge` must post exactly the body it always did; the optional
# autonomy= and upstream= fields add inputs of the same names.
set -uo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
fail=0
check() { # check <description> <command...>
  local d=$1
  shift
  if "$@" >/dev/null 2>&1; then echo "ok    $d"; else echo "FAIL  $d"; fail=1; fi
}

# A copy of the script in a bare tree: no bin/workspace, no state, no .env.
mkdir -p "$tmp/tree/bin" "$tmp/fakes"
cp "$root/bin/tend" "$tmp/tree/bin/tend"
cat >"$tmp/fakes/curl" <<'SH'
#!/usr/bin/env bash
# POST: record the -d body and answer an execution id. GET: completed.
body=""
while [ $# -gt 0 ]; do
  [ "$1" = "-d" ] && { body=$2; shift; }
  shift
done
if [ -n "$body" ]; then
  printf '%s\n' "$body" >>"$TEND_POSTS"
  printf '{"body":{"execution_id":"x1"}}\n200'
else
  printf '{"body":{"status":"completed"}}'
fi
SH
chmod +x "$tmp/fakes/curl"

run() { # run <list content>: the bodies posted, one per line
  : >"$tmp/posts"
  printf '%s\n' "$1" >"$tmp/list"
  TEND_POSTS=$tmp/posts TEND_LIST=$tmp/list GH_TOKEN=x DREAMS_INBOX=$tmp/none PATH="$tmp/fakes:$PATH" \
    "$tmp/tree/bin/tend" >"$tmp/out" 2>"$tmp/err"
  cat "$tmp/posts"
}

legacy='{"inputs":{"repo":"acme/app","auto_merge":"false","dry_run":"false"}}'
check "a two column line posts the legacy body" test "$(run 'acme/app false')" = "$legacy"
check "a two column auto line posts the legacy body" \
  test "$(run 'acme/app true')" = '{"inputs":{"repo":"acme/app","auto_merge":"true","dry_run":"false"}}'
check "autonomy is posted" \
  test "$(run 'acme/app false autonomy=propose')" = '{"inputs":{"repo":"acme/app","auto_merge":"false","dry_run":"false","autonomy":"propose"}}'
check "autonomy and upstream are posted" \
  test "$(run 'acme/app true autonomy=auto upstream=up/tool=3,o/x=2')" = '{"inputs":{"repo":"acme/app","auto_merge":"true","dry_run":"false","autonomy":"auto","upstream":"up/tool=3,o/x=2"}}'
check "an unknown ceiling skips the repository" test -z "$(run 'acme/app false autonomy=god')"
check "a bad upstream skips the repository" test -z "$(run 'acme/app false autonomy=auto upstream=nonsense')"
check "an unknown field skips the repository" test -z "$(run 'acme/app false foo=1')"

if [ "$fail" = 0 ]; then echo "tend test: all checks passed"; else echo "tend test: failures above" >&2; exit 1; fi
