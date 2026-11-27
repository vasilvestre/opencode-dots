#!/usr/bin/env bash
# Tests bin/propose-links with a fake curl and a fake repos binary: the opt-in file, what is
# posted for each workspace, what is skipped, and that a failed run gives a non-zero exit.
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

mkdir -p "$tmp/tree/bin" "$tmp/fakes" "$tmp/state"
cp "$root/bin/propose-links" "$tmp/tree/bin/propose-links"
cat >"$tmp/fakes/curl" <<'SH'
#!/usr/bin/env bash
# POST: record the -d body and answer an execution id. GET: the status in PL_STATUS.
body=""
while [ $# -gt 0 ]; do
  [ "$1" = "-d" ] && { body=$2; shift; }
  shift
done
if [ -n "$body" ]; then
  printf '%s\n' "$body" >>"$PL_POSTS"
  printf '{"body":{"execution_id":"x1"}}\n%s' "${PL_CODE:-200}"
else
  printf '{"body":{"status":"%s"}}' "${PL_STATUS:-completed}"
fi
SH
# A repos binary that knows the workspaces "default" and "other" only.
cat >"$tmp/fakes/repos" <<'SH'
#!/usr/bin/env bash
[ "$1 $2" = "db list" ] || exit 2
case "$4" in default|other) echo "acme/app	advisories" ;; esac
SH
chmod +x "$tmp/fakes/curl" "$tmp/fakes/repos"

list=$tmp/state/propose-links.list
# run <list content or NOFILE>: the bodies posted, one per line; the exit code goes to $tmp/rc.
# It runs in the caller's shell, so the caller captures it in a subshell and reads rc afterwards.
run() {
  : >"$tmp/posts"
  if [ "$1" = NOFILE ]; then rm -f "$list"; else printf '%s\n' "$1" >"$list"; fi
  PL_POSTS=$tmp/posts STATE_DIR=$tmp/state REPOS_BIN=$tmp/fakes/repos AWF_URL=http://awf.test PROPOSE_LINKS_POLL=0 \
    PATH="$tmp/fakes:$PATH" "$tmp/tree/bin/propose-links" >"$tmp/out" 2>"$tmp/err"
  echo $? >"$tmp/rc"
  cat "$tmp/posts"
}

out=$(run NOFILE)
check "no list file: exit 0 and nothing posted" test "$(cat "$tmp/rc")" = 0 -a -z "$out"
out=$(run ''); check "an empty list posts nothing" test -z "$out"
out=$(run '# only a comment'); check "a comment posts nothing" test -z "$out"
out=$(run 'default')
check "a workspace posts the workflow's workspace input" test "$out" = '{"inputs":{"workspace":"default"}}'
out=$(run $'default\nother')
check "one post per workspace, in order" test "$out" = $'{"inputs":{"workspace":"default"}}\n{"inputs":{"workspace":"other"}}'
out=$(run $'# note\n\ndefault')
check "comments and blank lines are skipped" test "$out" = '{"inputs":{"workspace":"default"}}'
out=$(run 'unknown'); check "a workspace the database does not know is skipped" test -z "$out"
out=$(run 'bad;name'); check "an invalid workspace name is skipped" test -z "$out"
out=$(run 'default extra'); check "a line with extra columns is skipped" test -z "$out"
out=$(PROPOSE_LINKS_MAX=3 run 'default')
check "PROPOSE_LINKS_MAX adds max_pairs" test "$out" = '{"inputs":{"workspace":"default","max_pairs":"3"}}'
out=$(PROPOSE_LINKS_MAX=99 run 'default')
check "a bad PROPOSE_LINKS_MAX posts nothing and fails" test -z "$out" -a "$(cat "$tmp/rc")" = 2
out=$(PL_STATUS=failed run 'default')
check "a failed run exits 1" test "$(cat "$tmp/rc")" = 1
out=$(PL_CODE=422 run $'default\nother')
check "an HTTP error exits 1 and the next workspace is still tried" test "$(cat "$tmp/rc")" = 1 -a "$(wc -l <<<"$out")" = 2
check "it never calls awf run" bash -c '! grep -vE "^[[:space:]]*#" "$1" | grep -Eq "awf run"' _ "$root/bin/propose-links"

if [ "$fail" = 0 ]; then echo "propose-links test: all checks passed"; else echo "propose-links test: failures above" >&2; exit 1; fi
