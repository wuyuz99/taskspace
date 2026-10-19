# Multiplexers

tspace keeps each task's agents and shells in a session of a terminal
multiplexer: one session per task, one window per repository plus the task
root. Which multiplexer is a user-wide choice:

```bash
tspace config multiplexer            # show the current one
tspace config multiplexer zellij     # switch
tspace config multiplexer --unset    # back to tmux
TSPACE_MULTIPLEXER=zellij tspace status   # override for one command or shell
```

The choice applies to every task at once: tspace looks for each task's session
in the chosen multiplexer. Stop running sessions (`tspace stop TASK`) before
switching; `tspace config multiplexer` names any that are still up.

## Built in

| | tmux (default) | zellij (0.44 or later) |
| --- | --- | --- |
| window | window | tab |
| exited command stays readable | `remain-on-exit` | held command pane |
| `tspace restart` | respawns the pane | reruns the held pane in place |
| `tspace attach` from inside | switches the client | switches the session |
| detach | prefix, then `d` | `Ctrl-o`, then `d` |

zellij cannot add a tab to a session that no client is attached to. The tab
gets no size and is dropped when a client attaches, while its process keeps
running. So tspace creates a zellij session from a single layout holding every
window, and refuses to add tabs (`tspace window`, or `tspace start` for a
repository added later) until you are attached. The same applies to
`tspace restart` after a window's command has changed; an unchanged command is
rerun in place without a client.

## Any other multiplexer: adapters

A name that is not built in is driven through an **adapter**, a program that
translates tspace's operations into that multiplexer's commands. tspace runs
`tspace-mux-NAME` from `PATH`, or the command you give it:

```bash
tspace config multiplexer wezterm                         # runs tspace-mux-wezterm
tspace config multiplexer wezterm ~/bin/wez-adapter --x   # runs this instead
```

Giving a command for a built-in name (`tspace config multiplexer tmux ~/bin/my-tmux`)
replaces the built-in support. Choosing the name again without a command
(`tspace config multiplexer tmux`, or `--unset` for tmux) forgets the command,
going back to the built-in support, or to `tspace-mux-NAME` for other names.

The adapter only translates. tspace checks the adapter's own state report
before every change, so an adapter never has to decide whether a window may be
started or relaunched. Those rules are enforced in one place:

- `start` creates only windows that are missing, so it is safe to re-run;
- `window` refuses a name that already exists;
- `restart` refuses a window whose command is still running.

[`tspace-mux-tmux.sh`](tspace-mux-tmux.sh) is a complete adapter in about 100
lines of POSIX sh. Start from it, and see `internal/runtime/adapter_real_test.go`
for how it is tested.

### Protocol 1

Every operation is one invocation, `ADAPTER OPERATION ARGS...`. Exit 0 for
success. Any other status is a failure, and tspace shows the adapter's stderr.

| Operation | Does |
| --- | --- |
| `check PROTOCOL` | Exit 0 if the multiplexer is usable and you speak `PROTOCOL` (currently `1`); otherwise explain on stderr. |
| `state SESSION` | Print `exists` or `missing` on the first line, then one line per pane: `NAME<TAB>live\|dead<TAB>COMMAND`. Several lines for one window (split panes) are fine; the window is live if any pane is. COMMAND, the program currently running, is informational and may be empty. |
| `new-session SESSION WIN` | Create a detached session whose first window is WIN. |
| `new-window SESSION WIN` | Add WIN to an existing session. |
| `respawn SESSION WIN` | Relaunch WIN, whose command has exited, in its existing window. |
| `stop SESSION` | End the session. Interrupt its processes and give them a moment before killing anything: an agent killed mid-write can corrupt a file. |
| `attach SESSION` | Hand the terminal to the session. stdin, stdout and stderr are the user's terminal. Inside the same multiplexer, switch the current client instead of nesting. |
| `focus SESSION NAME` | If tspace is running inside a client of this multiplexer, show window NAME and print `yes`. Otherwise print nothing, and tspace will offer to attach. |
| `select SESSION NAME` | Make NAME the window the next `attach` lands on. |
| `rename SESSION OLD NEW` | Optional. Rename window OLD to NEW. Without it, `tspace window --rename` fails with your error and everything else works. |

`WIN` is four arguments and the command: `NAME DIR keep|close -- COMMAND...`.

- `COMMAND` is never empty; for a shell window tspace passes the user's shell.
- `DIR` may be empty, meaning any directory.
- `keep` means the window must stay open after `COMMAND` exits, so its output
  can be read and `state` reports it `dead`. `close` lets it close.
- Names (`SESSION`, `NAME`) contain only `A-Z a-z 0-9 @ _ ~ -`. tspace has
  already escaped everything else.

Future protocol versions will be announced through `check`. An adapter that
sees a version it does not know should exit non-zero.

## Adding a built-in multiplexer

Implement `runtime.Runtime` (`internal/runtime/runtime.go`) and add one line
to the `builtins` map in `internal/runtime/registry.go`. Nothing outside the
`runtime` package knows which multiplexer is in use. The tmux and zellij
implementations show how each rule above is kept when the multiplexer itself
does not help. Each has fake-runner tests for the commands it builds, and a
test against the real program that is skipped when the program is not
installed.
