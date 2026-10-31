# Optional coding agents

[Back to the README](../README.md)

## Set your agent once

If you use the same agent for every task, make it the default:

```bash
tspace config agent claude --model opus   # everything after the command is its argv
tspace task create checkout-redesign      # root agent: claude --model opus
tspace config                             # show defaults and where they are stored
tspace config agent --unset
```

A new task's root agent comes from, in order: `task create --agent` or `--shell`,
then the `--project` recipe's agent, then this default, and otherwise your
shell. The result is recorded on the task, as recipes are, so changing the
default later leaves existing tasks alone; use `tspace task update TASK --agent` for
those. The default is stored in `$TSPACE_ROOT/config.json`. It sets only the
task's agent, which runs in the root window, or in a single-repository task's
one repository window; other repository windows remain shells unless given their
own `--agent`.

## Run an agent per repository

A repository can run its own agent instead of a shell:

```bash
tspace repo add checkout-redesign ~/src/backend  --agent claude
tspace repo add checkout-redesign ~/src/frontend --agent codex
```

The `_root` window is still created, running a shell unless the task has an
agent of its own. For an agent in a repository only now and then, `tspace window
TASK REPO --agent` keeps the repository's shell window as it is.

## Recover a stopped agent

```bash
tspace restart PAY-142 backend
tspace restart PAY-142 --root
```

Restart relaunches the configured command only when every pane in that window
is dead. A live agent or ad-hoc split causes refusal. Missing sessions or
windows should be created with `tspace start`. Restart replaces dead-window output,
so inspect it first; it never reruns setup.
Ordinary `start` continues to preserve existing windows, including dead ones.

`tspace restart TASK REPO --resume` (or `--root --resume`) brings the agent back
with its conversation instead of starting it fresh. Each agent does that its own
way and tspace knows none of them, so you tell it, next to the agent, one flag
per word as for `--agent`:

```bash
tspace config agent  claude --add-dir {context_dir}      # default for new tasks
tspace config resume claude --continue --add-dir {context_dir}

tspace task update PAY-142 --resume codex --resume resume --resume=--last
tspace repo update PAY-142 frontend --resume aider --resume=--restore-chat-history
```

The default resume command is recorded only on tasks that take the default agent,
and changing an agent forgets its resume command, so a resume never runs a
different program than the agent it belongs to. Agents that key a conversation
to its directory find it again because the window resumes where it ran.

Moving that directory, as `tspace task rename` and `tspace repo move` do, leaves
such a conversation behind, and tspace names the old directory so you can move it yourself. For
Claude Code it can do that for you: `tspace config carry-conversations claude`
has it rename Claude Code's folder for the old directory, under
`~/.claude/projects`, to the new one's once the move is done. It never merges
into a folder the new directory already has.

### Bringing windows back resumes them

Once a window has a resume command, every way of bringing it back uses it:
`tspace start`, `tspace open` and dash's `s`/`S` for a window that is not
there (after a computer restart, or `X`), and `tspace restart` and dash's
`r` for a dead one. A window is resumed only where tspace has launched its
agent before: not in a worktree just created, one only `start --no-runtime`
made, or a root window starting for the first time, since none holds a
conversation. Each says which happened (`backend  resumed: claude
--continue`, `resumed PAY-142/backend`), on the command line and dash's note
line.

What a resume command does where there is nothing to resume is up to the
agent. If it exits within a second, tspace starts the window again with
its own command and says so (`its resume command exited at once; started
it fresh`). `--fresh`, on `start`, `open` and `restart`, never resumes;
`restart --resume` still resumes or refuses. Windows without a resume
command, shells included, start as they always have.

See [signals and hooks](STATUS.md#signals) to report agent progress.
