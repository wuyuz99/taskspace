# Command reference

[Back to the README](../README.md)

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
| `tspace project save NAME --from TASK [--replace]` | Save a reusable repository recipe |
| `tspace project list` / `tspace project show NAME` | Inspect saved recipes |
| `tspace task create NAME [REPO...] [--agent CMD / --shell] [--start]` | Create a task; each REPO is `REPO[@BRANCH][:BASE]`; `--start` starts it too. Also `tspace new` and `task new` |
| `tspace task update NAME [--agent CMD / --shell] [--title ...]` | Update a task's root agent and details |
| `tspace task list [--json]` | List tasks |
| `tspace task show NAME [--json]` | Show a task's repos in detail |
| `tspace task rm NAME` | Delete the record; refuses while worktrees exist |
| `tspace repo add TASK REPO [--name --branch --base --agent --post-create]` | Attach a repository, by registered name or path |
| `tspace repo rm TASK REPO` | Detach a repository (leaves the worktree) |
| `tspace repo update TASK REPO [--agent CMD / --shell] [--post-create SH] [--branch NAME] [--base REF [--rebase]]` | Update a repository's agent, setup, branch or base |
| `tspace start TASK [REPO...]` | Create worktrees, launch the session |
| `tspace open TASK` | Prepare worktrees, show brief/handoff, and attach |
| `tspace restart TASK REPO` / `tspace restart TASK --root` [`--resume`] | Relaunch a dead repository or root window, optionally resuming its conversation |
| `tspace stop TASK` | Interrupt agents, end the session, keep worktrees |
| `tspace attach TASK` | Attach (or switch the client when already inside the multiplexer) |
| `tspace window TASK [REPO / --root] [--agent / -- CMD] [--name N [--save]]` | Open another window in the running session, and with `--save` on every start |
| `tspace window TASK --forget N` / `tspace window TASK --rename OLD NEW` | Forget a saved window; rename an extra window |
| `tspace shell TASK [REPO / --root]` | Interactive shell in a worktree or the task root |
| `tspace path TASK [REPO / --root]` | Print a worktree or task-root path, for `cd "$(...)"` |
| `tspace exec TASK -- CMD` | Run a command in every worktree |
| `tspace status [TASK] [--json / --watch]` | Runtime, git and signal state |
| `tspace dash` | Live full-screen view of every task; attach, start, restart, stop, open in a new tab |
| `tspace signal STATE [MESSAGE] [--task --repo / --root] [--quiet]` | Report an agent as `working`, `blocked`, `done` or `failed` |
| `tspace config hooks claude [--remove]` | Install the Claude Code hooks that call `tspace signal` |
| `tspace clean TASK [--delete-branch] [--force] [--rm]` | Remove worktrees, safely; `--rm` deletes the task record too |
| `tspace import PATH [--task NAME] [--name REPO]` | Move a worktree made with `git worktree add` into a task |
| `tspace editor TASK [--print]` | Write the task's `.code-workspace` and open it in your editor |

`--no-title` (or `TSPACE_NO_TITLE=1`) suppresses terminal tab titling.
