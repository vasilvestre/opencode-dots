#!/usr/bin/env bash
# Tests bin/watchers on two directories of generated watcher files.
#   test/watchers.sh
set -uo pipefail
cd "$(dirname "$0")/.."
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
mkdir "$tmp/here" "$tmp/there" "$tmp/other"
printf 'repos:\n  - repo: acme/a\n    workflow: review-pr\n  - repo: acme/b\n    workflow: reply-pr\n' >"$tmp/here/awf-gh.yaml"
printf 'repos:\n  - repo: acme/a\n    workflow: fix-ci\n  - repo: acme/c\n    workflow: review-pr\n' >"$tmp/there/awf-gh.pc.yaml"
printf 'repos:\n  - repo: acme/z\n    workflow: review-pr\n' >"$tmp/other/awf-gh.yaml"
printf 'repos:\n  - repo: acme/a\n    workflow: review-pr\n' >"$tmp/there/awf-gh.local.example.yaml"

fail=0
expect() { # expect <description> <exit> <substring> <command...>
  local d=$1 want=$2 sub=$3 out rc
  shift 3
  out=$("$@" 2>&1); rc=$?
  if [ "$rc" = "$want" ] && [[ $out == *"$sub"* ]]; then echo "ok    $d"; else echo "FAIL  $d (exit $rc, want $want; output: $out)"; fail=1; fi
}
export DOTS_WATCHERS_HERE=$tmp/here
expect "an overlap is reported with both workflows" 1 "acme/a" bin/watchers "$tmp/there"
expect "the workflows of each side are shown" 1 "fix-ci" bin/watchers "$tmp/there"
expect "a repository watched on one side only is not reported" 1 "BOTH" bin/watchers "$tmp/there"
expect "no overlap exits 0" 0 "no repository is watched on both machines" bin/watchers "$tmp/other"
expect "an unreadable remote is an error" 2 "cannot read" bin/watchers nobody@nonexistent.invalid
out=$(bin/watchers "$tmp/there" 2>&1 || true)
if grep -q "acme/b" <<<"$out" || grep -q "acme/c" <<<"$out"; then echo "FAIL  a repository on one side only was listed"; fail=1; else echo "ok    only the shared repository is listed"; fi
if [ "$fail" = 0 ]; then echo "watchers test: all checks passed"; else echo "watchers test: failures above" >&2; exit 1; fi
