#!/usr/bin/env bash
# Drives the demo fixture non-interactively, to confirm it proves what it claims.
# Not part of the test suite; a convenience for checking scripts/demo.sh.
set -u
AW="${1:-tspace}"
DEMO="${2:-$HOME/tspace-demo}"
BE="$DEMO/src/backend"
FE="$DEMO/src/frontend"

export TSPACE_ROOT="$DEMO/.tspacestate"
export TSPACE_NO_TITLE=1
export TMUX_TMPDIR="$DEMO/.tmux"
mkdir -p "$TMUX_TMPDIR"

step() { printf '\n\033[1m$ tspace %s\033[0m\n' "$*"; "$AW" "$@" 2>&1 | sed 's/^/  /'; }

step task create checkout-redesign
step repo add checkout-redesign "$BE" --post-create 'cp "$TSPACE_REPO_PATH/.env" .'
step repo add checkout-redesign "$FE"
step start checkout-redesign
step status

printf '\n\033[1m-- post_create worked? backend needs its gitignored .env --\033[0m\n'
(cd "$TSPACE_ROOT/worktrees/checkout-redesign/backend" && ./run.sh) 2>&1 | sed 's/^/  /'

printf '\n\033[1m-- sibling layout holds? frontend reads ../backend/api.json --\033[0m\n'
(cd "$TSPACE_ROOT/worktrees/checkout-redesign/frontend" && ./build.sh) 2>&1 | sed 's/^/  /'

printf '\n\033[1m-- the same task WITHOUT the hook --\033[0m\n'
"$AW" task create no-hook >/dev/null
"$AW" repo add no-hook "$BE" >/dev/null
"$AW" start no-hook --no-runtime >/dev/null
(cd "$TSPACE_ROOT/worktrees/no-hook/backend" && ./run.sh) 2>&1 | sed 's/^/  /'

step stop checkout-redesign
step clean checkout-redesign
step clean no-hook
step task rm checkout-redesign
step task rm no-hook
