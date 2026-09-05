#!/usr/bin/env bash
# End-to-end check of the aw vertical slice against real git repositories and a
# real tmux server.
#
# Usage: scripts/e2e.sh [path-to-aw-binary]
#
# It exercises the Definition of Done: create a task, attach two repos, create
# isolated worktrees, launch persistent agents, read status, run a command
# across every worktree, stop without losing work, refuse an unsafe clean, and
# clean safely.
set -uo pipefail

AW="${1:-aw}"
FAILED=0

WORK="$(mktemp -d)"
export AW_ROOT="$WORK/state"
export AW_NO_TITLE=1
export TMUX_TMPDIR="$WORK/tmux"
mkdir -p "$TMUX_TMPDIR"

cleanup() {
  tmux -L aw-e2e kill-server >/dev/null 2>&1
  rm -rf "$WORK"
}
trap cleanup EXIT

say()  { printf '\n\033[1m== %s\033[0m\n' "$*"; }
pass() { printf '   ok    %s\n' "$*"; }
fail() { printf '   FAIL  %s\n' "$*"; FAILED=$((FAILED+1)); }

check() { # check <description> <pattern> <text>
  if grep -q -- "$2" <<<"$3"; then pass "$1"; else
    fail "$1 (wanted /$2/)"
    printf '%s\n' "$3" | sed 's/^/         | /'
  fi
}

mkrepo() {
  local dir="$1"
  mkdir -p "$dir"
  git -C "$dir" init -q -b main
  git -C "$dir" config user.email t@example.invalid
  git -C "$dir" config user.name "aw e2e"
  echo "# $(basename "$dir")" > "$dir/README.md"
  git -C "$dir" add -A
  git -C "$dir" commit -qm initial
}

say "setup"
mkrepo "$WORK/src/backend"
mkrepo "$WORK/src/frontend"
# Park backend's HEAD on an unrelated branch: worktrees must fork from main.
git -C "$WORK/src/backend" checkout -q -b unrelated
echo leak > "$WORK/src/backend/leak.txt"
git -C "$WORK/src/backend" add -A
git -C "$WORK/src/backend" commit -qm "unrelated work"
echo "workdir: $WORK"

say "aw task create"
out="$($AW task create test-feature 2>&1)"; check "task created" "created task test-feature" "$out"

say "aw repo add (name derived from basename)"
out="$($AW repo add test-feature "$WORK/src/backend" 2>&1)"
check "backend added"        "added backend"              "$out"
check "branch convention"    "agent/test-feature"         "$out"
check "base resolved to main" "from main"                 "$out"

out="$($AW repo add test-feature "$WORK/src/frontend" --agent sleep --agent 600 2>&1)"
check "frontend added" "added frontend" "$out"

say "aw repo add rejects a non-repository"
mkdir -p "$WORK/not-a-repo"
out="$($AW repo add test-feature "$WORK/not-a-repo" 2>&1)"
check "non-repo refused" "not a git repository" "$out"

say "aw task show"
out="$($AW task show test-feature 2>&1)"
check "lists backend"  "backend"  "$out"
check "lists frontend" "frontend" "$out"

say "aw start"
out="$($AW start test-feature 2>&1)"
check "backend worktree created"  "backend.*created worktree"  "$out"
check "frontend worktree created" "frontend.*created worktree" "$out"
check "session reported"          "session aw@test-feature is up" "$out"

if [ -f "$AW_ROOT/worktrees/test-feature/backend/leak.txt" ]; then
  fail "worktree forked from HEAD, not the base branch (leak.txt present)"
else
  pass "worktree forked from the resolved base, not HEAD"
fi
[ -f "$AW_ROOT/worktrees/test-feature/backend/README.md" ] \
  && pass "worktree is populated" || fail "worktree is empty"

say "worktrees are on their own branch"
b="$(git -C "$AW_ROOT/worktrees/test-feature/backend" rev-parse --abbrev-ref HEAD)"
[ "$b" = "agent/test-feature" ] && pass "branch is $b" || fail "branch is $b"

say "aw status (two axes)"
out="$($AW status 2>&1)"
check "header has RUNTIME" "RUNTIME" "$out"
check "header has GIT"     "GIT"     "$out"
check "backend running"    "backend.*running"  "$out"
check "frontend running"   "frontend.*running" "$out"
check "clean worktrees"    "clean"             "$out"

say "aw status reports dirty and running independently"
echo "work in progress" > "$AW_ROOT/worktrees/test-feature/backend/wip.txt"
out="$($AW status test-feature 2>&1)"
check "dirty while running" "backend.*running.*dirty" "$out"

say "aw exec runs in every worktree"
out="$($AW exec test-feature -- git status --short 2>&1)"
check "visits backend"     "=== backend"  "$out"
check "visits frontend"    "=== frontend" "$out"
check "sees the wip file"  "wip.txt"      "$out"

say "aw path"
out="$($AW path test-feature backend 2>&1)"
[ "$out" = "$AW_ROOT/worktrees/test-feature/backend" ] \
  && pass "path prints only the path" || fail "path printed: $out"

say "aw start is idempotent and preserves work"
out="$($AW start test-feature 2>&1)"
check "worktree reused" "backend.*worktree ready" "$out"
[ -f "$AW_ROOT/worktrees/test-feature/backend/wip.txt" ] \
  && pass "uncommitted work survived restart" || fail "restart destroyed uncommitted work"

say "aw stop leaves worktrees alone"
out="$($AW stop test-feature 2>&1)"
check "stopped" "stopped test-feature" "$out"
[ -f "$AW_ROOT/worktrees/test-feature/backend/wip.txt" ] \
  && pass "work survived stop" || fail "stop destroyed work"

out="$($AW status test-feature 2>&1)"
check "runtime now no-session" "no-session" "$out"

say "aw clean refuses to destroy uncommitted work"
out="$($AW clean test-feature 2>&1)"
check "refused"            "refusing to clean"    "$out"
check "names the file count" "uncommitted change" "$out"
check "suggests a way out" "--force"              "$out"
[ -f "$AW_ROOT/worktrees/test-feature/backend/wip.txt" ] \
  && pass "refused clean changed nothing" || fail "refused clean still deleted work"

say "aw clean succeeds once the work is committed"
git -C "$AW_ROOT/worktrees/test-feature/backend" add -A
git -C "$AW_ROOT/worktrees/test-feature/backend" -c user.email=t@example.invalid \
    -c user.name=aw commit -qm "commit the wip"
out="$($AW clean test-feature 2>&1)"
check "backend worktree removed"  "backend.*removed worktree"  "$out"
check "frontend worktree removed" "frontend.*removed worktree" "$out"
check "record survives"           "task record is still there" "$out"

[ -d "$AW_ROOT/worktrees/test-feature/backend" ] \
  && fail "worktree directory still present" || pass "worktree directory gone"

say "clean kept the branch (it holds unmerged work)"
if git -C "$WORK/src/backend" show-ref --verify --quiet refs/heads/agent/test-feature; then
  pass "branch agent/test-feature survived clean"
else
  fail "clean deleted a branch holding unique work"
fi

say "--delete-branch still refuses unmerged, unpushed work"
out="$($AW clean test-feature --delete-branch 2>&1)"
check "branch kept with a reason" "kept branch" "$out"
if git -C "$WORK/src/backend" show-ref --verify --quiet refs/heads/agent/test-feature; then
  pass "unique work protected from --delete-branch"
else
  fail "--delete-branch destroyed unmerged, unpushed work"
fi

say "aw task rm"
out="$($AW task rm test-feature 2>&1)"
check "task removed" "removed task test-feature" "$out"
out="$($AW status 2>&1)"
check "no tasks left" "no tasks yet" "$out"

printf '\n'
if [ "$FAILED" -eq 0 ]; then
  printf '\033[1mall end-to-end checks passed\033[0m\n'
else
  printf '\033[1m%d end-to-end check(s) failed\033[0m\n' "$FAILED"
fi
exit "$FAILED"
