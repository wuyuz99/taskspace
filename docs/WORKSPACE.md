# Workspace and session layout

[Back to the README](../README.md)

## Why a task spans repositories

A single feature rarely lives in one repository. It touches `backend`,
`frontend`, and `infra` at the same time. Git gives you worktrees, which are
per-repository, so keeping a feature isolated means creating three worktrees on
three branches, opening terminals for the work, and
remembering which tab is which.

Most tools model this as:

```
repo -> worktree -> agent
```

`tspace` models it as:

```
task -> repos -> worktrees -> shells or optional agents
```

The **task** is the first-class object. You operate on `checkout-redesign`, not
on nine separate resources.

A task with several repositories, or none yet, has a window at its root, the directory
holding every worktree: run one agent there and it works across all the repositories,
while a shell in each repository is on hand to review and commit. A single-repository
task normally starts its task command in the repository window instead. Agents can also run in selected
repositories. `tspace` prepares and resumes the workspace; it does not coordinate
agents or schedule their work.

```
checkout-redesign
├── (root)                               claude   running
├── backend    agent/checkout-redesign   shell    running   dirty (3 files)
├── frontend   agent/checkout-redesign   shell    running   clean, ahead 2
└── infra      agent/checkout-redesign   shell    dead      clean
```

## Workspace structure

**Task** — a logical unit of work. The thing you actually think in. It has a
name, an optional agent command for its root window, and one entry per
repository.

**Repo entry** — a source repository enrolled in a task, together with its
branch (`agent/<task>` by default), the ref it was forked from, its worktree
path, an optional agent command, and an optional `post_create` hook.

**Worktree** — a real `git worktree`, laid out so the repos of a task are
siblings:

```
$TSPACE_ROOT/worktrees/checkout-redesign/
├── backend/
└── frontend/
```

Cross-repo relative paths (`../frontend`) keep working inside a task.

**Session** — one multiplexer session per task (`tspace@<encoded-task>`). When the
task has a root window, `_root` opens first in the task root and runs the task's
agent, or your shell if it has none. Then comes one window per repo, named after the repo. No repository
can be called `_root`: repository names start with a letter or digit. Closing
`tspace` never stops an agent.

**Single-repository tasks** normally have no `_root` window. A task created with one
repository (or given its first one with `tspace repo add` before it ever ran)
starts its agent in that repository's window, in the worktree, where the code
is. `tspace task create --with-root` keeps the root window instead. When
`tspace repo add` brings a second repository, the agent stays where it is, so
`claude --continue` still finds its conversation, and the task gains a `_root`
window running a shell: opened straight away if the session is running, else
by the next `start`. Removing repositories again never takes that window away.
`tspace task show` says which window runs the agent. That repository's agent is
the task's, so `tspace repo update TASK REPO --agent` changes the task's agent;
`--shell` there refuses rather than leave the task with no agent, and `tspace
task update TASK --shell` drops it on purpose. `tspace repo rm` closes that window and safely cleans its worktree before
forgetting it; `--keep-worktree` still requires stopping a running agent first. A repository
added with an `--agent` of its own keeps the root layout, so the task's agent
still has a window.

## Enter the whole workspace

`tspace open TASK` prepares missing worktrees and session windows, prints the task
brief and handoff, and attaches to the session (or switches clients inside the multiplexer).
Existing windows are preserved. Dead windows get an explicit `tspace restart` hint;
opening does not restart them automatically. Setup hooks run only for newly
created worktrees, as with `tspace start`. Add at least one repository before opening.

`tspace start TASK` and `tspace open TASK` create the `_root` window, if the task
has one, along with the repository windows. `tspace start TASK REPO...` starts only the named repositories.
A session started before root windows existed gains one, as its last window,
on the next full `start` or `open`.

```bash
tspace shell PAY-142
cd "$(tspace path PAY-142)"
```

The root contains the task's repository directories, useful for cross-repository
inspection or launching your preferred agent there. Without a repository argument,
`shell` and `path` use the root, unless the task has exactly one repository, in
which case they use its worktree. `--root` asks for the root explicitly and cannot
be combined with a repository. `shell` creates the root if necessary, even before
repositories are added, but does not create worktrees or start a session. `path`
only prints it. Shells expose `TSPACE_TASK_DIR`, `TSPACE_CONTEXT_DIR`, `TSPACE_TASK`,
`TSPACE_WORKTREE` (the current directory), `TSPACE_REPO` (empty at the task root),
and `TSPACE_ROOT`.
The root shell has a task-only terminal label.
