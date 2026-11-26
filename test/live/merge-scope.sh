#!/usr/bin/env bash
# Live check of the merge-scope rule with the identity marker (docs/e2e.md,
# "Live check"). The REAL review-pr workflow runs through the real awf, the real
# gh and the real git against a private scratch repository; only the agent is
# the stub test/e2e/fakes/opencode, which always writes a clean "merge" verdict
# and changes no code. Cases that merge really merge in the sandbox.
#
#   I_UNDERSTAND_THIS_MERGES_IN_THE_SANDBOX=1 test/live/merge-scope.sh
#
# Own temp dir (STATE_DIR, WORK_ROOT, awf storage, HOME, git config). It never
# talks to the live `awf serve` nor reads its state directory.
set -euo pipefail

SANDBOX=vasilvestre/dots-e2e-sandbox
REPO=${LIVE_REPO:-$SANDBOX}
[ "${I_UNDERSTAND_THIS_MERGES_IN_THE_SANDBOX:-}" = 1 ] \
  || { echo "refusing: set I_UNDERSTAND_THIS_MERGES_IN_THE_SANDBOX=1 (this merges pull requests in $SANDBOX)" >&2; exit 2; }
[ "$REPO" = "$SANDBOX" ] || { echo "refusing: repository must be exactly $SANDBOX" >&2; exit 2; }

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
GH_CFG=${GH_CONFIG_DIR:-$HOME/.config/gh}
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
mkdir -p "$T/state" "$T/work" "$T/home" "$T/stub" "$T/dots/bin" "$T/dots/workflows" "$T/proj/.awf" "$T/fake"
cp -r "$ROOT/workflows" "$T/proj/.awf/workflows"
ln -s "$ROOT/bin/publish-gate" "$T/dots/bin/publish-gate"
ln -s "$ROOT/bin/dot-sign" "$T/dots/bin/dot-sign"
ln -s "$ROOT/test/e2e/fakes/held" "$T/dots/bin/held"
ln -s "$ROOT/workflows/data" "$T/dots/workflows/data"
ln -s "$ROOT/test/e2e/fakes/opencode" "$T/stub/opencode"
: > "$T/repos.map"
cat > "$T/gitconfig" <<'CFG'
[user]
	name = live-check
	email = live-check@example.invalid
[credential "https://github.com"]
	helper = !gh auth git-credential
[advice]
	detachedHead = false
CFG

export GH_CONFIG_DIR=$GH_CFG GIT_CONFIG_GLOBAL=$T/gitconfig GIT_CONFIG_SYSTEM=/dev/null GIT_TERMINAL_PROMPT=0
unset DISCORD_WEBHOOK GH_TOKEN GITHUB_TOKEN DOT_LOGIN
ghr() { gh "$@" --repo "$REPO"; }

# new_pr KIND: a fresh pull request on a fix/<N>-* branch (the review-pr merge
# step only takes those, with a Closes #N body). KIND human: plain body; dot: marker.
new_pr() {
  local kind=$1 n=$RANDOM br body c="$T/clone"
  br=fix/$n-live$(date +%s)
  [ -d "$c" ] || git clone -q "https://github.com/$REPO.git" "$c"
  git -C "$c" checkout -q -b "$br" origin/main
  printf 'live check %s\n' "$br" > "$c/live-$n.txt"
  git -C "$c" add -A
  git -C "$c" commit -q -m "live check $n"
  git -C "$c" push -q origin "$br"
  body="Trivial change for the live merge-scope check.

Closes #$n"
  [ "$kind" = dot ] && body="$body

<!-- opencode-dots:dot=vasilvestre:fix-issue:$n -->"
  gh pr create --repo "$REPO" --base main --head "$br" --title "live check $n" --body "$body" | sed 's#.*/pull/##'
}

# run_case LABEL PR [GATE_MERGE_SCOPE] [IDENTITY]
run_case() {
  local label=$1 pr=$2 scope=${3:-} identity=${4-marker} sha branch iss rc=0
  sha=$(ghr pr view "$pr" --json headRefOid --jq .headRefOid)
  branch=$(ghr pr view "$pr" --json headRefName --jq .headRefName)
  case "$branch" in
    fix/[0-9]*-*) iss=${branch#fix/}; iss=${iss%%-*}
      mkdir -p "$T/state/fix/$REPO/issue-$iss"; printf '1\n' > "$T/state/fix/$REPO/issue-$iss/item_class" ;;
  esac
  : > "$T/fake/agent.log"
  echo "=== $label: PR $pr branch $branch identity='$identity' scope='${scope:-own}'"
  (
    cd "$T/proj"
    export PATH="$T/stub:$PATH" HOME="$T/home" FAKE_DIR="$T/fake" FAKE_REVIEW=clean-merge \
      STATE_DIR="$T/state" WORK_ROOT="$T/work" REPOS_MAP="$T/repos.map" DOTS_HOME="$T/dots" \
      CI_WAIT_SECONDS=2 GATE_MERGE_SCOPE="$scope"
    awf run review-pr --storage "$T/awf" --output buffered \
      --input "repo=$REPO" --input "pr=$pr" --input "sha=$sha" \
      --input autonomy=auto --input "identity=$identity"
  ) > "$T/out.log" 2>&1 || rc=$?
  echo "awf exit $rc; stub agent calls: $(grep -c . "$T/fake/agent.log" || true)"
  grep -E 'publish-gate: merge|automerge:|autonomy: not merging' "$T/out.log" | sed 's/^/  /' || echo "  (no gate or automerge line)"
  [ "$rc" = 0 ] || tail -n 15 "$T/out.log" | sed 's/^/  | /'
  echo "  $(ghr pr view "$pr" --json state,mergedAt)"
}

H6=$(ghr pr view 6 --json state --jq .state)
P5=$(ghr pr view 5 --json state --jq .state)
echo "PR 6 (human): $H6; PR 5 (dot): $P5"

# (a0) the existing human PR: its branch is not fix/*, stopped before the gate.
if [ "$H6" = OPEN ]; then run_case "a0 human PR 6" 6 "" marker; fi
# (a) human, fix/* branch + Closes #N so the gate is reached; marker required.
HUMAN=$(new_pr human)
run_case "a human, identity=marker" "$HUMAN" "" marker
# (b) dot PR with marker.
if [ "$P5" = OPEN ]; then DOT=5; else DOT=$(new_pr dot); fi
run_case "b dot, identity=marker" "$DOT" "" marker
# (c) same human PR, marking off.
run_case "c human, identity off" "$HUMAN" "" ""
# (d) fresh human PR, scope all.
HUMAN2=$(new_pr human)
run_case "d human, GATE_MERGE_SCOPE=all" "$HUMAN2" all marker
