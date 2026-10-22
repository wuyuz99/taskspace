# Moving between task windows

[Back to the README](../README.md)

One tspace task is a tmux **session**. Its repository terminals, `_root`, and
extra `_NAME` terminals are **windows**. If you split a window, those subdivisions
are panes. The default Ctrl-b w picker includes windows from every session;
with ten tasks, it is easy to lose the task boundary.

For a picker restricted to the session you are in, optionally add this binding
to your tmux configuration. Uppercase W is separate from the default lowercase w:

```tmux
bind-key W run-shell "tmux -S '#{socket_path}' choose-tree -w -t '#{pane_id}' -f '##{==:##{session_id},#{session_id}}'"
```

Reload your configuration yourself when ready. tspace does not install bindings.
Use Ctrl-b W, choose `backend` or `_logs`, and press Enter. Right expands a window
to its panes; q cancels. The picker operates while an agent occupies the pane:
the prefix is handled by tmux, without typing into the agent.

For a deliberate switch **between tasks**, use the default Ctrl-b s. It starts
with sessions collapsed. Select `tspace@OTHER` and press Enter to switch the
existing client; this does not nest tmux. This picker also includes non-tspace
sessions. Use `tspace dash` when you need task status or want to start a stopped
task, which has no live session to select.

The binding captures the invoking session ID before evaluating each picker
candidate. It does not trust `TSPACE_TASK`, a current working directory, or a
root inherited from another process. The doubled hashes defer the candidate
expression until the picker runs; single quotes protect tmux's `$N` session
IDs from the shell. Keep those quotes. A custom socket path containing a single
quote needs appropriate shell quoting.

## Investigation and verification

Verified on tmux 3.4 under WSL, using an isolated server with the default
bindings, ten sessions and three windows each (`_root`, `backend`, `_logs`).
The foreground process was `sleep`, so navigation did not depend on a shell
prompt. `scripts/check-tmux-navigation.py` repeats the check using a terminal:

```sh
python3 scripts/check-tmux-navigation.py
```

The scoped picker excludes the other nine tasks, includes extra windows, and
q leaves the original window and process intact. The exact binding is tested
as Ctrl-b W input. Ctrl-b s displays all ten tasks. Identity comes from tmux,
so a separate tspace state root does not affect this within-session lookup.

This is smaller than adding another task picker: native tree mode already
includes extra windows and split panes that the dashboard does not display.
A dashboard popup would add lifecycle/status controls but would need its own
extra-window rows, focus/cancel behavior and popup compatibility. Keep that as
a separate feature if native navigation proves insufficient. No new UI is
needed for the reported within-task navigation problem.

The native picker shows live runtime objects; it does not resume a stopped
task. A window that disappears cannot be selected; reopen it with tspace. The
automated check covers scope and cancel, not every race with concurrent window
closure. These bindings are tmux-specific; they do not configure Zellij.

The options and tree-mode behavior are described in the
[tmux manual](https://man.openbsd.org/tmux.1#choose-tree).
