#!/usr/bin/env bash
# Harness for bin/test-e2e. Source it; it defines helpers, runs nothing.
#
# One case = one scratch tree under $E2E_TMP:
#   proj/.awf/workflows   the workflows under test, run by the real `awf`
#   state, work           STATE_DIR and WORK_ROOT of the run
#   remotes/tester/demo.git   the "origin": a local bare repository. A private
#                         git config maps https://github.com/ onto remotes/, so
#                         the workflows' own clone/fetch/push URLs hit it.
#   fake/                 gh.log, agent.log, posted/, canned gh answers (fake/gh)
#   dots                  DOTS_HOME: the real bin/publish-gate, a fake bin/held and bin/budget
# PATH starts with test/e2e/fakes (gh, opencode, curl). Nothing here reads the
# network or a credential.

E2E_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(cd "$E2E_DIR/../.." && pwd)
REPO=tester/demo
LOGIN=tester

PASS=0
FAIL=0
declare -a FAILED=()

say() { printf '%s\n' "$*"; }

# check NAME COMMAND...: pass when the command succeeds.
check() {
  local name=$1
  shift
  if "$@" >/dev/null 2>&1; then
    PASS=$((PASS + 1))
    say "ok    $name"
  else
    FAIL=$((FAIL + 1))
    FAILED+=("$name")
    say "FAIL  $name"
  fi
}

# --- case construction -------------------------------------------------------

git_seed() { git -C "$T/seed" "$@"; }

# case_new LABEL [WORKFLOWS_DIR]
case_new() {
  CASE=$1
  local wf=${2:-$REPO_ROOT/workflows}
  mkdir -p "${E2E_TMP:?}"
  T=$(mktemp -d "$E2E_TMP/case.XXXXXX")
  mkdir -p "$T/proj/.awf" "$T/state" "$T/work" "$T/home" "$T/fake/gh" "$T/dots/bin" "$T/remotes/tester"
  cp -r "$wf" "$T/proj/.awf/workflows"
  : > "$T/repos.map"
  ln -s "$REPO_ROOT/bin/publish-gate" "$T/dots/bin/publish-gate"
  ln -s "$REPO_ROOT/bin/patch-guard" "$T/dots/bin/patch-guard"
  ln -s "$REPO_ROOT/bin/sandbox-test" "$T/dots/bin/sandbox-test"
  ln -s "$REPO_ROOT/bin/dot-sign" "$T/dots/bin/dot-sign"
  ln -s "$E2E_DIR/fakes/held" "$T/dots/bin/held"
  ln -s "$E2E_DIR/fakes/budget" "$T/dots/bin/budget"
  mkdir -p "$T/dots/workflows"
  ln -s "$REPO_ROOT/workflows/data" "$T/dots/workflows/data"
  cat > "$T/gitconfig" <<EOF
[url "$T/remotes/"]
	insteadOf = https://github.com/
[user]
	name = seed
	email = seed@example.invalid
[init]
	defaultBranch = main
[protocol "file"]
	allow = always
[advice]
	detachedHead = false
EOF
  export GIT_CONFIG_GLOBAL=$T/gitconfig GIT_CONFIG_SYSTEM=/dev/null
  git init -q --bare "$T/remotes/tester/demo.git"
  git clone -q "$T/remotes/tester/demo.git" "$T/seed" 2>/dev/null
  printf 'line1\n' > "$T/seed/src.txt"
  printf '# demo\n' > "$T/seed/README.md"
  git_seed add -A
  git_seed commit -q -m "initial"
  git_seed branch -M main
  git_seed push -q origin main
  printf '%s\n' "$LOGIN" > "$T/fake/gh/login"
  jq -n --arg r "$REPO" '{full_name:$r, default_branch:"main", permissions:{push:true}, defaultBranchRef:{name:"main"}}' \
    > "$T/fake/gh/repo.json"
  set_issue 7 "Crash on empty input" "It crashes when the input is empty."
  : > "$T/fake/gh.log"
  : > "$T/fake/agent.log"
  : > "$T/fake/agent.steps"
}

# set_issue NUMBER TITLE BODY
set_issue() {
  jq -n --argjson n "$1" --arg t "$2" --arg b "$3" \
    '{number:$n, title:$t, body:$b, author:{login:"reporter"}, labels:[], state:"OPEN", comments:[]}' \
    > "$T/fake/gh/issue.json"
}

# seed_pr NUMBER BRANCH AUTHOR [BODY]: a branch on origin with one commit, the
# pull ref the review fetches, and the canned `gh pr view` answer.
seed_pr() {
  local n=$1 branch=$2 author=$3 body=${4:-"Fixes the crash.

Closes #7"}
  git_seed checkout -q -b "$branch" main
  printf 'line2\n' >> "$T/seed/src.txt"
  git_seed commit -q -am "pr change"
  git_seed push -q origin "$branch"
  PR_SHA=$(git_seed rev-parse HEAD)
  git -C "$T/remotes/tester/demo.git" update-ref "refs/pull/$n/head" "$PR_SHA"
  git_seed checkout -q main
  jq -n --arg b "$body" --arg br "$branch" --arg a "$author" --arg sha "$PR_SHA" --arg r "$REPO" --argjson n "$n" \
    '{number:$n, title:"Fix the crash", body:$b, url:("https://github.com/" + $r + "/pull/" + ($n|tostring)),
      author:{login:$a}, baseRefName:"main", headRefName:$br, headRepository:{nameWithOwner:$r},
      state:"OPEN", headRefOid:$sha, mergeable:"MERGEABLE", createdAt:"2026-01-01T00:00:00Z", comments:[]}' \
    > "$T/fake/gh/pr-$n.json"
  printf 'diff --git a/src.txt b/src.txt\n+line2\n' > "$T/fake/gh/diff.patch"
  # A fix/N-* branch is one fix-issue opened: its run left the class of issue N
  # (SEED_CLASS, default 1; "none" leaves no file). Other branches carry no class.
  case "$branch" in
    fix/[0-9]*-*)
      local iss=${branch#fix/}
      iss=${iss%%-*}
      if [ "${SEED_CLASS:-1}" != none ]; then
        mkdir -p "$T/state/fix/$REPO/issue-$iss"
        printf '%s\n' "${SEED_CLASS:-1}" > "$T/state/fix/$REPO/issue-$iss/item_class"
      fi ;;
  esac
}

# --- running -----------------------------------------------------------------

# run_wf WORKFLOW [key=value ...]: the real awf, in the scratch project. Sets RC.
run_wf() {
  local wf=$1 args=() kv
  shift
  for kv in "$@"; do args+=(--input "$kv"); done
  (
    cd "$T/proj" || exit 99
    export PATH="$E2E_DIR/fakes:$PATH" HOME="$T/home" FAKE_DIR="$T/fake" \
      STATE_DIR="$T/state" WORK_ROOT="$T/work" REPOS_MAP="$T/repos.map" DOTS_HOME="$T/dots" \
      GIT_CONFIG_GLOBAL="$T/gitconfig" GIT_CONFIG_SYSTEM=/dev/null GIT_ALLOW_PROTOCOL=file \
      GIT_TERMINAL_PROMPT=0 CI_WAIT_SECONDS=2
    unset DISCORD_WEBHOOK GH_TOKEN GITHUB_TOKEN
    awf run "$wf" --storage "$T/awf" --output buffered "${args[@]}"
  ) > "$T/out.$wf.log" 2>&1
  RC=$?
  cp "$T/out.$wf.log" "$T/out.log"
}

# --- observations ------------------------------------------------------------

gh_count() { grep -cE "$1" "$T/fake/gh.log" || true; }   # ERE on a log line
gh_none()  { [ "$(gh_count "$1")" = 0 ]; }
gh_some()  { [ "$(gh_count "$1")" -ge 1 ]; }
gh_once()  { [ "$(gh_count "$1")" = 1 ]; }

origin_refs() { git -C "$T/remotes/tester/demo.git" for-each-ref --format='%(refname) %(objectname)'; }
has_branch()  { [ -n "$(git -C "$T/remotes/tester/demo.git" for-each-ref --format='%(refname)' "refs/heads/$1")" ]; }
no_branch()   { ! has_branch "$1"; }
no_push_no_pr() { no_branch "fix/*" && gh_none "^gh pr create"; }
branch_file() { git -C "$T/remotes/tester/demo.git" show "$1:$2"; }  # BRANCH FILE

n_files() { find "$1" -maxdepth 1 -type f -name "$2" 2>/dev/null | wc -l; }
note_of() { n_files "$T/state/notes" "*-$1-*"; }          # records by action
held_of() { n_files "$T/state/held" "*-$1-*"; }
no_net()  { [ ! -s "$T/fake/net.log" ] && gh_none '^UNHANDLED'; }
agent_ran()  { grep -q "^agent $1" "$T/fake/agent.steps"; }
agent_idle() { ! grep -q "^agent $1" "$T/fake/agent.steps"; }

# the one held record of an action, as a file name
held_id() { ls "$T/state/held" 2>/dev/null | grep -E -- "-$1-" | grep -v '^released' | head -1; }

# The call log with run-specific noise folded, to compare two runs.
norm_log() {
  sed -E -e "s#$T#<T>#g" -e 's#fix/([0-9]+)-[0-9]{8}-[0-9]{4}#fix/\1-<ts>#g' "$T/fake/gh.log"
}

case_done() {
  [ -z "${E2E_KEEP:-}" ] && rm -rf "$T" || say "      (kept $T)"
}

summary() {
  say ""
  say "passed $PASS, failed $FAIL"
  if [ "$FAIL" -gt 0 ]; then
    printf 'FAILED: %s\n' "${FAILED[@]}"
    return 1
  fi
  return 0
}
