#!/usr/bin/env bash
# Build a small multi-repo playground for trying aw by hand.
#
# Usage: scripts/demo.sh [dir]      (default: ~/aw-demo)
#        scripts/demo.sh --clean [dir]
#
# The two repos are rigged to fail in the two ways real projects fail inside a
# fresh worktree, so the demo tests the design rather than just exercising it:
#
#   1. backend needs a .env that is gitignored, so it is NOT in a new worktree.
#      This is what --post-create exists for.
#   2. frontend reads ../backend/api.json, so it only works if the repos of a
#      task are siblings. This is what the <task>/<repo> layout exists for.
set -euo pipefail

CLEAN=0
if [ "${1:-}" = "--clean" ]; then CLEAN=1; shift; fi
ROOT="${1:-$HOME/aw-demo}"

if [ "$CLEAN" -eq 1 ]; then
  rm -rf "$ROOT"
  echo "removed $ROOT"
  echo "note: task records and worktrees live under AW_ROOT (default ~/.aw)"
  exit 0
fi

if [ -e "$ROOT" ]; then
  echo "$ROOT already exists; re-run with --clean first, or pass another path." >&2
  exit 1
fi

git_in() { git -C "$1" "${@:2}"; }

mkrepo() {
  local dir="$1"
  mkdir -p "$dir"
  git_in "$dir" init -q -b main
  git_in "$dir" config user.email demo@example.invalid
  git_in "$dir" config user.name "aw demo"
}

commit() { git_in "$1" add -A; git_in "$1" commit -qm "$2"; }

echo "creating demo repositories under $ROOT"
mkdir -p "$ROOT/src"

# ---------------------------------------------------------------- backend ---
BE="$ROOT/src/backend"
mkrepo "$BE"

cat > "$BE/.gitignore" <<'EOF'
.env
node_modules/
EOF

cat > "$BE/api.json" <<'EOF'
{
  "service": "checkout",
  "version": 1,
  "endpoints": ["POST /checkout", "GET /checkout/:id"]
}
EOF

cat > "$BE/run.sh" <<'EOF'
#!/usr/bin/env bash
# Fails without .env — exactly like a real service.
set -euo pipefail
if [ ! -f .env ]; then
  echo "backend: FAIL - .env is missing (it is gitignored, so a fresh worktree has none)" >&2
  exit 1
fi
. ./.env
echo "backend: ok - running against ${DATABASE_URL:-unset}"
EOF
chmod +x "$BE/run.sh"

cat > "$BE/README.md" <<'EOF'
# backend (demo)

Needs a local `.env`, which is gitignored. Copy `.env.example` to `.env`.
EOF

cat > "$BE/.env.example" <<'EOF'
DATABASE_URL=postgres://localhost/checkout
EOF

commit "$BE" "backend: initial"

# The developer's real, uncommitted local config. This is the file a fresh
# worktree will not have.
cat > "$BE/.env" <<'EOF'
DATABASE_URL=postgres://localhost/checkout_dev
EOF

# --------------------------------------------------------------- frontend ---
FE="$ROOT/src/frontend"
mkrepo "$FE"

cat > "$FE/.gitignore" <<'EOF'
node_modules/
EOF

cat > "$FE/build.sh" <<'EOF'
#!/usr/bin/env bash
# Reads the backend's contract through a relative path, so it only works when
# the two repos sit side by side.
set -euo pipefail
CONTRACT="../backend/api.json"
if [ ! -f "$CONTRACT" ]; then
  echo "frontend: FAIL - cannot see $CONTRACT (backend is not a sibling)" >&2
  exit 1
fi
echo "frontend: ok - built against $(grep -o '"service": *"[^"]*"' "$CONTRACT")"
EOF
chmod +x "$FE/build.sh"

cat > "$FE/README.md" <<'EOF'
# frontend (demo)

Builds against `../backend/api.json`, so it expects the backend repo alongside it.
EOF

commit "$FE" "frontend: initial"

# ------------------------------------------------------------------ done ----
cat <<EOF

demo repos ready:
  $BE
  $FE

backend has an uncommitted .env; frontend needs backend as a sibling.

Try it:

  aw task create checkout-redesign
  aw repo add checkout-redesign $BE \\
      --post-create 'cp "\$AW_REPO_PATH/.env" .'
  aw repo add checkout-redesign $FE

  aw start checkout-redesign
  aw status

  # proves post_create worked and the sibling layout holds
  aw exec checkout-redesign -- sh -c './run.sh 2>/dev/null || ./build.sh'

  aw attach checkout-redesign     # interactive: Ctrl-b d to detach
  aw shell checkout-redesign backend

  aw stop checkout-redesign
  aw clean checkout-redesign

To see what the hook is for, add the backend a second time without it:

  aw task create no-hook
  aw repo add no-hook $BE
  aw start no-hook
  aw exec no-hook -- ./run.sh        # fails: no .env in the worktree

Remove everything:
  scripts/demo.sh --clean $ROOT
  rm -rf ~/.aw            # or \$AW_ROOT
EOF
