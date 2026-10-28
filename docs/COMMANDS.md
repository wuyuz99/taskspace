# Command reference

[Back to the README](../README.md)

Inside a task directory or worktree, `.` names that task for start/open,
restart, attach, shell, path, exec, window, editor, task show/update/brief/handoff,
repo add/update, status, and project save's `--from`. The directory, resolved
through symlinks, decides; inherited `TSPACE_TASK` does not. `editor`, `attach`,
`task show`, and `exec -- COMMAND` may omit TASK entirely. `status` with no task
still shows all tasks. `stop`, `clean`, `task rm` and `repo rm` require explicit
task names.

## Commands

| Command | What it does |
| --- | --- |
| `tspace init` | Create/report state directories (never required) |
| `tspace config` / `tspace config agent [CMD ARGS... / --unset]` | Show defaults; set the default task-root agent |
| `tspace config resume [CMD ARGS... / --unset]` | Set how the default agent resumes, for `restart --resume` |
| `tspace config repo [NAME [PATH / URL [--into DIR]] / NAME --unset]` | Register source repositories by name; a URL is cloned first |
| `tspace config branch-prefix [PREFIX / --unset]` | Set the prefix of new task branches (default `agent/`) |
| `tspace config ticket-pattern [REGEXP / --unset]` | Read new tasks' tickets from their names |
| `tspace config terminal [CMD ARGS... / --unset]` | How dash's `t` opens a task in a new terminal tab |
| `tspace config editor [CMD ARGS... / --unset]` | The editor `tspace editor` opens a task's workspace with |
| `tspace config multiplexer [NAME [CMD ARGS...] / --unset]` | Choose tmux, zellij or an adapter; see [Multiplexers](MULTIPLEXERS.md) |
| `tspace project save NAME --from TASK [--replace]` | Save a reusable repository recipe |
| `tspace project list` / `tspace project show NAME` | Inspect saved recipes |
| `tspace task create NAME [REPO...] [--agent CMD / --shell] [--project P] [--with-root] [--start]` | Create a task; each REPO is `REPO[@BRANCH][:BASE]`; `--start` starts it too. Also `tspace new` and `task new` |
| `tspace task update NAME [--agent CMD / --shell] [--title ...]` | Update a task's root agent and details |
| `tspace task list [--json]` | List tasks |
| `tspace task show [NAME] [--json]` | Show a task's repos in detail |
| `tspace task brief NAME [TEXT / --file F / --edit / --clear]` | Show or replace the task's brief |
| `tspace task handoff NAME [TEXT / --file F / --edit / --clear]` | Show or replace the task's handoff |
| `tspace task rm NAME [--force]` | Delete the record; refuses while worktrees exist unless `--force` |
| `tspace repo add TASK REPO [--name --branch --base --agent --post-create]` | Attach a repository, by registered name or path |
| `tspace repo rm TASK REPO [--keep-worktree]` | Clean the repository's worktree and windows safely, then detach it; `--keep-worktree` only forgets it |
| `tspace repo update TASK REPO [--agent CMD / --shell] [--post-create SH] [--branch NAME] [--base REF [--rebase]]` | Update a repository's agent, setup, branch or base |
| `tspace start TASK [REPO... / --root] [--no-runtime]` | Create worktrees, launch the session; naming REPOs or `--root` starts only those windows; `--no-runtime` stops after the worktrees |
| `tspace open TASK` | Prepare worktrees, show brief/handoff, and attach |
| `tspace restart TASK REPO` / `tspace restart TASK --root` [`--resume`] | Relaunch a dead repository or root window (or saved `_WINDOW`), optionally resuming its conversation |
| `tspace stop TASK [REPO / _WINDOW / --root]` | Interrupt agents, end the session (or only that window), keep worktrees |
| `tspace attach [TASK]` | Attach (or switch the client when already inside the multiplexer) |
| `tspace window TASK [REPO / --root] [--agent / -- CMD] [--name N [--save]] [-d]` | Open another window in the running session, and with `--save` on every start |
| `tspace window TASK --forget N` / `tspace window TASK --rename OLD NEW` | Forget a saved window; rename an extra window |
| `tspace shell TASK [REPO / --root]` | Interactive shell in a worktree or the task root |
| `tspace path TASK [REPO / --root]` | Print a worktree or task-root path, for `cd "$(...)"` |
| `tspace exec [TASK] -- CMD` | Run a command in every worktree |
| `tspace status [TASK] [--json / --watch]` | Runtime, git and signal state |
| `tspace dash` | Live full-screen view of every task; create, attach, start, restart, stop, clean, delete, remove a repository, open in a new tab |
| `tspace signal STATE [MESSAGE] [--task --repo / --root] [--quiet] [--clear]` | Report an agent as `working`, `blocked`, `done` or `failed` |
| `tspace config hooks claude [--remove]` | Install the Claude Code hooks that call `tspace signal` |
| `tspace clean TASK [REPO...] [--delete-branch] [--force] [--rm]` | Remove worktrees, safely, of every repository or only those named; `--rm` deletes the task record too |
| `tspace import PATH [--task NAME] [--name REPO]` | Move a worktree made with `git worktree add` into a task |
| `tspace editor [TASK] [--print]` | Write the task's `.code-workspace` and open it in your editor |
| `tspace completion SHELL` | Generate a shell completion script |

`--no-title` (or `TSPACE_NO_TITLE=1`) suppresses terminal tab titling.
