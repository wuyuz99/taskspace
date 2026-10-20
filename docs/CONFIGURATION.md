# Configuration and reusable projects

[Back to the README](../README.md)

## Reusable projects

Save a working task's repository configuration once, then reuse it for new tickets:

```bash
tspace project save payments --from PAY-142
tspace project list
tspace project show payments
tspace task create PAY-156 --project payments --title "Add payment retry"
tspace open PAY-156
```

Recipes live in `$TSPACE_ROOT/projects/<name>.json`. They copy the task-root agent,
repository names and local source paths, base refs, agent argv, and setup hooks.
`task create --project payments --agent codex` (or `--shell`) replaces the
recipe's root agent for the new task. Worktree paths and
branches are generated for the new task (`agent/PAY-156`); source task titles,
tickets, briefs, and handoffs are not copied. No worktrees or agents are created
until `start` or `open`.

Each task snapshots the recipe. To update the recipe for future tasks, run
`tspace project save payments --from TASK --replace`; existing tasks remain unchanged.
Saving over an existing recipe without `--replace` is refused. Use `project show`
to inspect commands and hooks before applying a recipe. Saving never executes them.

`task create PAY-156 --project payments infra` can add repositories to the
snapshot. Duplicate repository names or paths are rejected; they do not
silently override recipe entries. Validation failures leave no partial task record.
Use `task update --agent` or `repo update` to customize an inherited agent or hook. These recipes reference local checkouts; they
do not clone repositories or synchronize configuration across machines. Recorded
base refs are reused; no fetch is performed. An empty base is resolved at creation.

### Update repository settings

```bash
tspace repo update PAY-142 backend --agent codex
tspace repo update PAY-142 backend --post-create 'pnpm install'
tspace repo update PAY-142 backend --shell
tspace repo update PAY-142 backend --post-create=''
tspace task update PAY-142 --agent claude --agent=--model --agent opus
tspace task update PAY-142 --shell
tspace project save payments --from PAY-142 --replace
```

`task update --agent` and `--shell` set the task-root window's command the same
way `repo update` sets a repository's.

Only supplied settings change. Repeat `--agent` for command arguments; `--shell`
clears the agent command, and an empty `--post-create` clears the hook. Existing
windows keep running their original command. Updated agent settings apply when
a window is created or explicitly restarted; updated hooks run only for newly
created worktrees. Updating a task does not change its saved project recipe;
save it back explicitly when desired. A repository's path is never changed by
`repo update`; its branch and base are, on request:

```bash
tspace repo update PAY-142 backend --branch me/PAY-142             # rename the task's branch
tspace repo update PAY-142 backend --base origin/release           # compare with release instead
tspace repo update PAY-142 backend --base origin/release --rebase  # and move the work onto it
```

`--branch` renames the branch, checked out in the worktree if there is one. It
never takes over a branch that already exists, locally or on a remote, never
renames one checked out outside the task, such as in your own clone, and drops
the old name's upstream, saying how to push the new one. `--base` changes what status and
clean compare the branch with, and what `start` creates it from, and is
recorded as a branch, tag or commit, never something like `HEAD~1` that moves
on its own; on its own it leaves the branch where it is. `--rebase` moves the branch's commits from the
old base onto the new one, in a clean worktree; a rebase that stops on a
conflict is undone, the record keeps the old base, and the error gives the git
command to do it by hand.

## Where things live

```
$TSPACE_ROOT/                          default ~/.tspace, override with TSPACE_ROOT
├── config.json                    user-wide defaults, shown by tspace config
├── repos/<name>/                  repositories cloned by tspace config repo NAME URL
├── tasks/<name>.json              task identity and repository configuration
├── projects/<name>.json           reusable repository recipes
└── worktrees/<task>/
    ├── .context/TASK.md           the brief, readable from any worktree
    ├── .context/HANDOFF.md        the handoff
    ├── .context/signals/          what each window's agent last reported
    ├── .context/TASK.code-workspace   written by tspace editor
    └── <repo>/                    the worktrees
```

Only declarative metadata is stored. **Runtime state is never written down** —
whether a session is up and whether a worktree is dirty are asked of the multiplexer and
git every time. Nothing can go stale, and there is no repair command.

Records, recipes and notes are written `0600`: your notes are your own material,
and not something to publish to every account on the machine. A file that cannot
be read or parsed is reported on stderr and skipped, so one damaged record never
hides the rest of your workspaces from `tspace task list` or `tspace status`.

Commands that change a task take that task's lock first, so two of them never
interleave: a title change and a `repo add` at once both land, `clean` waits for
a `start` that is creating worktrees instead of removing them under it, and a
command that waited for `task rm` finds the task gone rather than bringing it
back. A command that has to wait says who it is waiting for; Ctrl-C stops
waiting. Different tasks never wait for each other, and reading (`status`,
`task show`, printing a brief or handoff, `dash`) never waits at all. Actions
started from `dash` wait for each other the same way.

The lock is an OS file lock on `tasks/.locks/TASK.lock`, so it goes away with
its process however that ends; there is nothing to clean up after a crash.
`open` lets go before it attaches. A `post_create` hook runs while `start`
holds the lock and may still update the task (`tspace task update`, `task
handoff`), but not start, clean or otherwise change its worktrees or session,
which `start` is in the middle of doing. Git commands and editors you run
yourself are not covered.

### Record formats and upgrades

Each task record says which format it is in (`"version"`); one without is from
before versions. Reading a record never rewrites it: `status`, `task list` and
`task show` work on old records as they are. The first command that changes a
task brings its record up to date, under the task's lock, and every step of
that finishes its other files (such as moving inline notes out) before the
record is replaced. If one fails, the record is left as it was and the next
change tries again.

A record written by a newer tspace than yours is refused with a message saying
so, and left untouched; `task list` reports it and shows the rest. So going
back to an older tspace after a format change locks it out of the records
written since rather than letting it damage them. A tspace from before versions
reads today's records as it always did, since their fields are unchanged.

A record is written in the oldest format that holds it: version 2 only while
the task has saved windows, so every other task stays readable by a tspace
that knows version 1.

`config.json`, project recipes and signals have no version: they only gain
optional fields, which older and newer tspace alike ignore or leave empty.

`TSPACE_ROOT` exists because you probably already keep workspace directories
somewhere visible, and a hidden `~/.tspace` should not be forced on that habit.
With more than one root, a command run inside a root it is not using says so
on stderr and names the `TSPACE_ROOT` that would use it; it never switches on
its own. The windows tspace opens, `tspace shell` and `post_create` hooks get
`TSPACE_ROOT` set to the root that started them, so an agent's `tspace signal`
and `tspace task handoff` reach the right state whatever environment the
multiplexer was started with. A pane you split or open yourself has the
multiplexer's environment instead.
