#!/usr/bin/env bash
# The head repository of a pull request, read from `gh pr view --json headRepository,headRepositoryOwner`.
# gh 2.46 (Debian 13) has no `nameWithOwner` there, newer gh has: the workflows must read both, or a
# fetch goes to https://github.com/null.git. This checks the expression they share, and that no
# workflow reads the bare field.
set -uo pipefail
cd "$(dirname "$0")/.."
fail=0
ko() { echo "FAIL $1"; fail=1; }
expr=$(grep -ho '(\.headRepository\.nameWithOwner // (if [^)]*end))' workflows/review-pr.yaml | head -1)
[ -n "$expr" ] || { echo "FAIL no portable expression in review-pr.yaml"; exit 1; }
old='{"headRepository":{"id":"R","name":"app"},"headRepositoryOwner":{"login":"acme"}}'
new='{"headRepository":{"id":"R","name":"app","nameWithOwner":"acme/app"},"headRepositoryOwner":{"login":"acme"}}'
gone='{"headRepository":null,"headRepositoryOwner":null}'
[ "$(jq -r "$expr" <<<"$old")" = acme/app ] && echo "ok   gh 2.46 shape gives owner/name" || ko "old shape"
[ "$(jq -r "$expr" <<<"$new")" = acme/app ] && echo "ok   newer gh shape gives owner/name" || ko "new shape"
[ "$(jq -r "$expr" <<<"$gone")" = null ] && echo "ok   a deleted fork still gives null, as before" || ko "deleted fork"
bare=$(grep -n '\.headRepository\.nameWithOwner' workflows/*.yaml | grep -vc '(\.headRepository\.nameWithOwner //')
[ "$bare" = 0 ] && echo "ok   no workflow reads the bare nameWithOwner" || ko "$bare bare use(s)"
for f in workflows/*.yaml; do
  grep -q 'headRepository\.nameWithOwner' "$f" && ! grep -q 'headRepositoryOwner' "$f" && ko "$f reads the owner but never asks gh for it"
done
exit $fail
