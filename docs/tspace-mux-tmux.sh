#!/bin/sh
# tspace-mux-tmux: a reference tspace multiplexer adapter, driving tmux.
#
# tspace has tmux support built in; this script exists to show how small an
# adapter is. Copy it, keep the case arms, and replace the tmux commands with
# your multiplexer's. See docs/MULTIPLEXERS.md for the protocol.
#
#   cp docs/tspace-mux-tmux.sh ~/bin/tspace-mux-mytmux && chmod +x ~/bin/tspace-mux-mytmux
#   tspace config multiplexer mytmux
set -eu

op=${1:?usage: tspace-mux-tmux OPERATION ARGS...}
shift

# Exact-match targets: without '=', tmux would let "tspace@pay" match
# "tspace@payments".
target() { printf '=%s' "$1"; }
wtarget() { printf '=%s:=%s' "$1" "$2"; }

case $op in
check)
	[ "${1:-}" = 1 ] || { echo "tspace-mux-tmux speaks protocol 1, not ${1:-none}" >&2; exit 1; }
	tmux -V >/dev/null
	;;

state)
	if ! tmux has-session -t "$(target "$1")" 2>/dev/null; then
		echo missing
		exit 0
	fi
	echo exists
	tmux list-panes -s -t "$(target "$1")" \
		-F '#{window_name}	#{?pane_dead,dead,live}	#{pane_current_command}	#{?window_bell_flag,bell,}	#{window_activity}'
	;;

new-session | new-window | respawn)
	# WIN: NAME DIR keep|close -- COMMAND...
	session=$1 name=$2 dir=$3 keep=$4
	shift 5
	cd "${dir:-.}"
	case $op in
	new-session)
		tmux new-session -d -s "$session" -n "$name" -c "$PWD" /bin/sh
		;;
	new-window)
		tmux new-window -d -t "$(target "$session")" -n "$name" -c "$PWD" /bin/sh
		;;
	esac
	w=$(wtarget "$session" "$name")
	# Set before the command runs, so even a command that exits at once
	# leaves its output on screen.
	tmux set-option -w -t "$w" automatic-rename off
	[ "$keep" = keep ] && tmux set-option -w -t "$w" remain-on-exit on
	if [ "$op" = respawn ]; then
		# No -k: tmux itself refuses to replace a pane that is still alive.
		# respawn-pane: respawn-window would make the window the active one.
		tmux respawn-pane -t "$w" -c "$PWD" "$@"
	else
		tmux respawn-pane -k -t "$w" -c "$PWD" "$@"
	fi
	;;

close)
	w=$(wtarget "$1" "$2")
	for pane in $(tmux list-panes -t "$w" -F '#{pane_id} #{pane_dead}' | awk '$2 == 0 { print $1 }'); do
		tmux send-keys -t "$pane" C-c || :
	done
	sleep 2
	tmux kill-window -t "$w" 2>/dev/null || :
	;;

stop)
	for pane in $(tmux list-panes -s -t "$(target "$1")" -F '#{pane_id} #{pane_dead}' | awk '$2 == 0 { print $1 }'); do
		tmux send-keys -t "$pane" C-c || :
	done
	sleep 2
	tmux kill-session -t "$(target "$1")" 2>/dev/null || :
	;;

attach)
	if [ -n "${TMUX:-}" ]; then
		exec tmux switch-client -t "$(target "$1")"
	fi
	exec tmux attach-session -t "$(target "$1")"
	;;

focus)
	# Only a client inside tmux can be pointed at a window; say nothing
	# otherwise, and tspace will offer to attach instead.
	[ -n "${TMUX:-}" ] || exit 0
	tmux switch-client -t "$(wtarget "$1" "$2")"
	echo yes
	;;

select)
	tmux select-window -t "$(wtarget "$1" "$2")"
	;;

rename)
	tmux rename-window -t "$(wtarget "$1" "$2")" "$3"
	;;

*)
	echo "tspace-mux-tmux: unknown operation $op" >&2
	exit 2
	;;
esac
