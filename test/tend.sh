#!/usr/bin/env bash
# Tests what bin/tend posts to awf serve for each tend.list line. A line with only
# `repo auto_merge` must post exactly the body it always did; the optional
# autonomy=, upstream=, identity= and budget= fields add inputs of the same names (budget is
# posted as daily_usd=N, and a repository over its cap is skipped, ADR 0036).
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
check "identity is posted" \
  test "$(run 'acme/app false identity=signed')" = '{"inputs":{"repo":"acme/app","auto_merge":"false","dry_run":"false","identity":"signed"}}'
check "all three fields are posted" \
  test "$(run 'acme/app true autonomy=act upstream=up/tool=3 identity=marker')" = '{"inputs":{"repo":"acme/app","auto_merge":"true","dry_run":"false","autonomy":"act","upstream":"up/tool=3","identity":"marker"}}'
check "an unknown identity skips the repository" test -z "$(run 'acme/app false identity=loud')"
check "an unknown ceiling skips the repository" test -z "$(run 'acme/app false autonomy=god')"
check "a bad upstream skips the repository" test -z "$(run 'acme/app false autonomy=auto upstream=nonsense')"
check "an unknown field skips the repository" test -z "$(run 'acme/app false foo=1')"

# Daily budget (ADR 0036): a fake bin/budget answers with FAKE_BUDGET_RC and logs what it was asked.
cat >"$tmp/fakes/budget" <<'SH'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$TEND_BUDGET_CALLS"
echo "budget: fake says $FAKE_BUDGET_RC" >&2
exit "${FAKE_BUDGET_RC:-0}"
SH
chmod +x "$tmp/fakes/budget"
: >"$tmp/calls"
brun() { # brun <rc> <list content>: the bodies posted, with BUDGET_BIN answering <rc>
  local rc=$1
  shift
  : >"$tmp/posts"
  printf '%s\n' "$1" >"$tmp/list"
  FAKE_BUDGET_RC=$rc BUDGET_BIN=$tmp/fakes/budget TEND_BUDGET_CALLS=$tmp/calls \
    TEND_POSTS=$tmp/posts TEND_LIST=$tmp/list GH_TOKEN=x DREAMS_INBOX=$tmp/none PATH="$tmp/fakes:$PATH" \
    "$tmp/tree/bin/tend" >"$tmp/out" 2>"$tmp/err"
  cat "$tmp/posts"
}
check "a budget under its cap is posted as an input" \
  test "$(brun 0 'acme/app false autonomy=act budget=5')" = '{"inputs":{"repo":"acme/app","auto_merge":"false","dry_run":"false","autonomy":"act","budget":"daily_usd=5"}}'
check "a budget with decimals is posted as written" \
  test "$(brun 0 'acme/app true autonomy=auto budget=12.5')" = '{"inputs":{"repo":"acme/app","auto_merge":"true","dry_run":"false","autonomy":"auto","budget":"daily_usd=12.5"}}'
check "the budget check is asked for the repository and the cap" grep -qx 'check acme/app 5' "$tmp/calls"
check "a budget reached skips the repository" test -z "$(brun 3 'acme/app false autonomy=act budget=5')"
check "the skip says why" grep -q 'skip acme/app: not tended, daily budget' "$tmp/out"
check "spend that cannot be read skips the repository" test -z "$(brun 1 'acme/app false autonomy=act budget=5')"
check "a bad budget skips the repository" test -z "$(brun 0 'acme/app false autonomy=act budget=lots')"
check "a budget of three decimals skips the repository" test -z "$(brun 0 'acme/app false autonomy=act budget=1.234')"
: >"$tmp/calls"
check "a line without budget posts the legacy body even when the check would fail" \
  test "$(brun 3 'acme/app false')" = "$legacy"
check "a line without budget never runs bin/budget" test ! -s "$tmp/calls"
check "a legacy line with autonomy and identity is unchanged by the budget code" \
  test "$(brun 3 'acme/app false autonomy=act identity=marker')" = '{"inputs":{"repo":"acme/app","auto_merge":"false","dry_run":"false","autonomy":"act","identity":"marker"}}'
check "one repository over budget does not stop the next" \
  test "$(brun 3 $'acme/a false budget=5\nacme/b false')" = '{"inputs":{"repo":"acme/b","auto_merge":"false","dry_run":"false"}}'

if [ "$fail" = 0 ]; then echo "tend test: all checks passed"; else echo "tend test: failures above" >&2; exit 1; fi
