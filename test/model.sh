#!/usr/bin/env bash
# Tests bin/model against presets files of the shape the dashboard writes.
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
cat >"$tmp/presets.yaml" <<'YAML'
version: 1
presets:
    light: acme/small
    default: acme/big#thinking
    claude_light: haiku
    claude_default: "sonnet[1m]"
overrides:
    review-pr/address_model: other/model-2
YAML
m() { MODEL_PRESETS=$tmp/presets.yaml bin/model "$@"; }

check "a role takes its preset" test "$(m ask-pr/model light)" = acme/small
check "a variant is kept" test "$(m tend-repo/model default)" = "acme/big#thinking"
check "an override wins over the preset" test "$(m review-pr/address_model light)" = other/model-2
check "a quoted claude name loses its quotes" test "$(m review-pr/claude_review_model claude_default)" = "sonnet[1m]"
check "an unknown preset fails" bash -c "! MODEL_PRESETS=$tmp/presets.yaml bin/model ask-pr/model nope"
check "a missing file fails" bash -c "! MODEL_PRESETS=$tmp/none bin/model ask-pr/model light"
check "two arguments are required" bash -c "! bin/model ask-pr/model"

if [ "$fail" = 0 ]; then echo "model test: all checks passed"; else echo "model test: failures above" >&2; exit 1; fi
