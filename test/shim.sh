#!/usr/bin/env bash
# Tests bin/shim/opencode against a stub `opencode`, so nothing calls a model.
#   test/shim.sh
set -uo pipefail
cd "$(dirname "$0")/.."
shim=$PWD/bin/shim
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
mkdir "$work/stub"
# The stub: a model starting with "quota/" fails with a rate limit and no output, "late/"
# prints something and then fails with a rate limit, "broken/" fails for another reason.
cat >"$work/stub/opencode" <<'STUB'
#!/usr/bin/env bash
echo "stub: $*" >>"$STUB_LOG"
model=
for ((i = 1; i <= $#; i++)); do
  case "${!i}" in
    --model | -m) j=$((i + 1)); model=${!j} ;;
    --model=*) model=${!i#--model=} ;;
  esac
done
case "$model" in
  quota/*) echo "Error: 429 rate limit exceeded" >&2; exit 1 ;;
  late/*) echo "working..."; echo "Error: 429 rate limit exceeded" >&2; exit 1 ;;
  broken/*) echo "Error: Model unavailable: $model" >&2; exit 1 ;;
esac
echo "done with $model"
STUB
chmod +x "$work/stub/opencode"
# The shim goes first: it finds the stub as the "real" opencode by dropping its own directory.
export STUB_LOG=$work/log PATH="$shim:$work/stub:$PATH"

fail=0
run() { # run <name> <expected exit> <expected stdout substring> <expected log lines> -- env/args
  local name=$1 want_rc=$2 want_out=$3 want_calls=$4
  shift 5
  : >"$STUB_LOG"
  local out rc
  out=$(env "$@" 2>/dev/null)
  rc=$?
  local calls
  calls=$(wc -l <"$STUB_LOG")
  if [ "$rc" = "$want_rc" ] && [[ $out == *"$want_out"* ]] && [ "$calls" = "$want_calls" ]; then
    echo "ok    $name"
  else
    echo "FAIL  $name (rc=$rc want $want_rc, calls=$calls want $want_calls, out=$out)"
    fail=1
  fi
}

run "no fallback list: untouched, adds the agent" 0 "done with ok/a" 1 -- opencode run --model ok/a hi
run "a quota error falls over to the next model" 0 "done with ok/b" 2 -- DOTS_MODEL_FALLBACKS=ok/b opencode run --model quota/a hi
run "-m is understood" 0 "done with ok/b" 2 -- DOTS_MODEL_FALLBACKS=ok/b opencode run -m quota/a hi
run "--model=x is understood" 0 "done with ok/b" 2 -- DOTS_MODEL_FALLBACKS=ok/b opencode run --model=quota/a hi
run "walks a list of two" 0 "done with ok/c" 3 -- DOTS_MODEL_FALLBACKS="quota/b,ok/c" opencode run --model quota/a hi
run "stops when the list is spent" 1 "" 2 -- DOTS_MODEL_FALLBACKS=quota/b opencode run --model quota/a hi
run "another error is not retried" 1 "" 1 -- DOTS_MODEL_FALLBACKS=ok/b opencode run --model broken/a hi
run "a run that already printed is not retried" 1 "working..." 1 -- DOTS_MODEL_FALLBACKS=ok/b opencode run --model late/a hi
run "no model argument: nothing to swap" 0 "done with " 1 -- DOTS_MODEL_FALLBACKS=ok/b opencode run hi
run "a model that works is run once" 0 "done with ok/a" 1 -- DOTS_MODEL_FALLBACKS=ok/b opencode run --model ok/a hi
run "other subcommands pass through" 0 "done with " 1 -- DOTS_MODEL_FALLBACKS=ok/b opencode models

: >"$STUB_LOG"
DOTS_MODEL_FALLBACKS=ok/b opencode run --model quota/a hi >/dev/null 2>&1
if grep -q -- "--agent dots-worker" "$STUB_LOG" && [ "$(grep -c -- '--agent dots-worker' "$STUB_LOG")" = 2 ]; then
  echo "ok    every attempt carries --agent dots-worker"
else
  echo "FAIL  the agent flag is missing from an attempt"; cat "$STUB_LOG"; fail=1
fi

# The private server option: --standalone on every attempt when asked, and never otherwise.
: >"$STUB_LOG"
opencode run --model ok/a hi >/dev/null 2>&1
if ! grep -q -- "--standalone" "$STUB_LOG"; then echo "ok    no --standalone by default"; else echo "FAIL  --standalone added without being asked"; fail=1; fi
: >"$STUB_LOG"
DOTS_OPENCODE_STANDALONE=1 opencode run --model ok/a hi >/dev/null 2>&1
if grep -q -- "--agent dots-worker --standalone" "$STUB_LOG"; then echo "ok    --standalone on a plain run"; else echo "FAIL  --standalone missing from a plain run"; cat "$STUB_LOG"; fail=1; fi
: >"$STUB_LOG"
DOTS_OPENCODE_STANDALONE=1 opencode run hi >/dev/null 2>&1
if grep -q -- "--standalone" "$STUB_LOG"; then echo "ok    --standalone on a run with no model"; else echo "FAIL  --standalone missing when there is no model argument"; fail=1; fi
: >"$STUB_LOG"
DOTS_OPENCODE_STANDALONE=1 DOTS_MODEL_FALLBACKS=ok/b opencode run --model quota/a hi >/dev/null 2>&1
if [ "$(grep -c -- "--standalone" "$STUB_LOG")" = 2 ]; then echo "ok    --standalone on every fallback attempt"; else echo "FAIL  --standalone missing from a fallback attempt"; cat "$STUB_LOG"; fail=1; fi

if [ "$fail" = 0 ]; then echo "shim test: all checks passed"; else echo "shim test: failures above" >&2; exit 1; fi
