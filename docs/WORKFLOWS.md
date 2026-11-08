# Working in a task

[Back to the README](../README.md)

## A fuller workflow

```bash
# once: your agent, and the repositories you work in
tspace config agent claude
tspace config repo backend  ~/src/backend
tspace config repo frontend ~/src/frontend
tspace config repo infra    ~/src/infra
tspace config repo docs     git@github.com:acme/docs.git   # cloned into $TSPACE_ROOT/repos/docs

# one logical task spanning the repositories it needs: a new branch in
# backend, a colleague's existing branch in frontend, and a new branch in
# infra forked from develop
tspace task create checkout-redesign backend frontend@feature/cart infra:develop

# create the worktrees, show the task context and enter its session
tspace open checkout-redesign

# one session: the agent's _root window first, then a shell per repo
tspace attach checkout-redesign        # detach: Ctrl-b d in tmux, Ctrl-o d in zellij

# more windows in the same session, as the work needs them
tspace window checkout-redesign backend --agent             # an agent in backend only
tspace window checkout-redesign --name staging              # a spare shell at the root
tspace window checkout-redesign infra -- kubectl port-forward svc/api 8080:80

# what is going on, across every task
tspace status

# poke at one repo by hand without leaving your workflow
tspace shell checkout-redesign backend
cd "$(tspace path checkout-redesign frontend)"

# do the same thing in every worktree
tspace exec checkout-redesign -- git push -u origin HEAD

# stop the agents; nothing on disk is touched
tspace stop checkout-redesign

# tear the worktrees down, safely
tspace clean checkout-redesign

# or, when the task is finished, tear it down and delete its record too
tspace clean checkout-redesign --rm
```

Repositories can also be given as paths (`tspace task create T ~/src/backend`), and
added later with `tspace repo add TASK REPO`.

## Open more windows as you go

`tspace window` adds a window to a running task session without touching the
windows already there — an agent in one repository while the root agent keeps
working, a spare shell, or a long-running command against staging:

```bash
tspace window PAY-142 backend --agent       # the task's agent, in backend's worktree
tspace window PAY-142 --name staging        # your shell, at the task root
tspace window PAY-142 infra -- ./scripts/tail-staging-logs
tspace window PAY-142 backend -d -- make watch   # open it without switching to it
```

Without `REPO`, a task with one repository opens that worktree and any other
task opens its root, as with `shell`. `--agent` runs the task's root agent, or
for a task whose root runs a shell, the default from `tspace config agent`.

Extra windows are named with a leading `_` — `_backend-claude`,
`_staging` — so they never take a repository's window name, and a name already
in use is refused rather than duplicated. A shell window closes when you exit
it; a command's window stays open after the command exits, so its output can
still be read. Inside the multiplexer you are switched to the new window. `stop`
ends extra windows with the rest of the session.

Windows you want every time — lazygit, a log tail, a spare shell — can be saved
with the task. `start` and `open` then recreate them whenever they are not
there, and project recipes saved from the task carry them to new tasks:

```bash
tspace window PAY-142 backend --name git --save -- lazygit   # open it now, and on every start
tspace window PAY-142 --rename git lg                        # rename it, open and saved alike
tspace window PAY-142 --forget lg                            # start no longer opens it
```

`--save` needs `--name`, which the window is recreated by; with no session
running it only saves the window, so it can be set up before the first start.
Starting some repositories (`tspace start TASK REPO`) opens only their saved
windows, and `repo rm` forgets the ones that opened in the repository it
removes. `task show` lists them. `--rename` works on any extra window, saved
or not; the root and repository windows are named by the task.

An unsaved window is remembered while its session lasts, so `tspace restart
TASK _NAME` relaunches it when it is dead, and one worth keeping can be saved
as it stands:

```bash
tspace window PAY-142 --save _backend-lazygit   # start opens it from now on
```

`start` never reopens an unsaved window, and recipes never carry one. tspace
cannot know what runs in a window opened by an older tspace or with the
multiplexer's own keys; close it and open it with `--save -- COMMAND` instead.

`tspace shell` is the other way to get a terminal: a plain subshell in the terminal
you are already in, labelled and with `TSPACE_*` variables set, whether or not the
session is running.

## Work with ordinary shells

For a manual workflow without persistent terminals or agent commands:

```bash
tspace task create checkout-review "backend=$HOME/src/backend" "frontend=$HOME/src/frontend"
tspace start checkout-review --no-runtime
tspace shell checkout-review
# a task with several repositories opens at its root: backend/ and frontend/
# are available in this shell; exit to return
```

## Every worktree in one editor window: `editor`

`tspace editor TASK` writes the task's worktrees, and its notes, into one
`.code-workspace` file and opens it with your editor. The format is VS Code's
multi-root workspace, which the editors built on it read too; tell tspace yours
once:

```bash
tspace config editor code          # or cursor, codium --new-window, ...
tspace editor PAY-142
tspace editor PAY-142 --print      # write it and print its path; open it yourself
```

The file is `.context/TASK.code-workspace`, beside the notes, which appear in it
as `.context`. Each run brings its folders up to date with the task, so a
repository added or removed shows up the next time; folders you add from
outside the task, your `settings`, `extensions` and anything else in the file
are kept, and a file with nothing to change is not touched. A worktree not
created yet is left out until `start` makes it, and a file with comments in it,
which tspace cannot rewrite faithfully, is left alone. `clean` removes the file
with the worktrees it lists. `editor` starts and removes nothing: tspace keeps
the worktrees, the editor its windows and terminals.

Run it where the editor runs. From WSL, `code` opens the workspace through VS
Code's WSL support, with the paths as WSL sees them; on a remote machine, the
paths are that machine's, for an editor connected to it.

## On a remote machine, over SSH

Run tspace where the code and the agents are. Install it on that machine, as in
[Installation](INSTALL.md), with git and a multiplexer, and connect:

```bash
ssh -t dev-box tspace
```

A command given to `ssh` runs in a shell that is not a login shell, so it sees
only the system's `PATH`. If tspace or the multiplexer is in `~/.local/bin`, or
`TSPACE_ROOT` is set in `~/.profile`, start a login shell instead:
`ssh -t dev-box 'bash -lc tspace'`.

`tspace` with no command opens dash there. Sessions belong to the multiplexer on
that machine, so agents keep running when you disconnect, or when your laptop
sleeps, and dash shows them when you come back. Detaching from a session returns
you to dash; quitting dash and closing the connection leaves everything running.

A few things differ from working locally:

- State, worktrees and notes are on the remote machine, and each machine has its
  own tasks. There is no dash across machines.
- dash's `t` runs `config terminal` on the machine dash runs on, which has no
  terminal of yours to open a tab in. Use `enter` instead, or connect again
  and run `tspace attach TASK`.
- `tspace editor TASK --print` writes the workspace file and prints its path,
  for an editor connected to that machine to open.
- A multiplexer server keeps the environment it started with. If you forward an
  SSH agent, a later connection's agent may not reach windows already open.
  Credentials stored on the remote machine avoid that.

## Repositories the task only reads: `--ref`

A feature often needs another repository only to read: a shared library, the
API's schema, the infra repository. Add it as a reference:

```bash
tspace task create PAY-142 backend --ref schema
tspace repo add PAY-142 shared:origin/v2 --ref
```

`start` checks a reference out in the task's directory like the others, at its
base and detached, and makes its files read-only, so an edit fails whether an
agent's edit tool or a shell command tries it. It has no branch, window, agent
or `post_create`: the root window's agent sees it, and a task with one
repository and a reference gets a root window, as with two. `status` shows how
far behind its base it is; `tspace repo update PAY-142 schema --base
origin/main` fetches and moves it there. `clean` removes it without looking for
a branch, but refuses if something was changed in it anyway. To work in it after
all, `tspace repo update PAY-142 schema --branch me/schema` puts it on a new
branch where it stands and makes its files writable; `tspace start PAY-142
schema` then opens its window.

## Adopting a worktree you already have: `import`

A worktree you made yourself with `git worktree add` can become a task's:

```bash
tspace import ../backend-fix                   # a new task, named backend-fix
tspace import ~/wt/api-login --task PAY-142    # into an existing task
tspace import ../api ../web --task PAY-142     # several, all or none
```

`import` moves the worktree into the task's directory with `git worktree move`,
so uncommitted changes and its `.env`, `node_modules` and other untracked files
come along, and records its repository and branch. Nothing is committed. Anything that wrote its own absolute path into
those files, such as a Python `.venv`, needs rebuilding after the move.

Run it from outside the worktree. It refuses conflicted files, a detached HEAD, an unfinished merge or rebase, a locked worktree, a
worktree that is already a task's, a bare repository's worktree, and a
repository's main checkout, which cannot be moved: register that with
`tspace config repo` instead. Without `--task` the task is new; an existing one
is joined only by naming it. Commits not pushed or not pulled are reported and
kept. The base is the branch's upstream when that is another branch, and
otherwise, as after `git push -u`, the repository's default branch.

Several paths need `--task`, and each repository is named after its source, so
`--name` is for one path. All are checked before any moves, and a failure after
that, such as a base that no longer resolves, moves back the ones already moved. The layout is decided once, after all of them, as
for `task create`: a task with no repositories gets its agent in the worktree of
a single import, and at the task root with several, or with `--with-root`, which
is for a new task. A task that has repositories keeps its agent where it is.

## Fresh worktrees are empty: `post_create`

A new worktree has your tracked files and nothing else. No `.env`, no
`node_modules`, no `venv`, no local certificates. This surprises everyone once.

```bash
tspace repo add checkout-redesign ~/src/backend \
  --post-create 'cp "$TSPACE_REPO_PATH/.env" . && pnpm install'
```

The hook runs once, in the new worktree, the first time it is created, with
these variables set:

| Variable | Value |
| --- | --- |
| `TSPACE_REPO_PATH` | the source repository the worktree came from |
| `TSPACE_WORKTREE` | the new worktree (also the working directory) |
| `TSPACE_TASK_DIR` | the task's workspace directory, parent of every repo |
| `TSPACE_CONTEXT_DIR` | the task's `.context/` directory, holding its notes |
| `TSPACE_TASK`, `TSPACE_REPO` | the task and repo names |
| `TSPACE_ROOT` | the state root the task belongs to |

Use `$TSPACE_REPO_PATH` rather than a relative path like `../../../src/backend`.
How many `..` you need depends on where `TSPACE_ROOT` is, so a relative hook breaks
for anyone who moves their state directory.

If the hook fails you get a warning and a worktree you can inspect — `tspace` will
not delete your worktree because a provisioning step failed.

### Application and service isolation

Worktrees separate checked-out source files. They share the source repository's
Git object database, refs, and stash stack. Applications in different tasks can
still use the same listening port, database, container name, or external account.
`tspace` does not allocate these resources or rewrite application configuration.
Copying a source `.env` also copies its service destinations.

For an application that reads `DATABASE_PATH` and `UPLOAD_DIR` from `.env`, a
setup hook can put its SQLite database and uploads inside its own worktree:

```bash
tspace repo add checkout-review ~/src/backend --post-create '
  mkdir -p .local/uploads
  printf "DATABASE_PATH=.local/app.sqlite\nUPLOAD_DIR=.local/uploads\nWORKSPACE_LABEL=%s/%s\n" "$TSPACE_TASK" "$TSPACE_REPO" > .env
'
```

This example assumes the application resolves these paths from its worktree and
ignores `.env` and `.local/` in Git. The task and repository names label the
configuration; separate worktree paths keep each database and upload directory
independent. Adapt the variable names to your application. If it listens on a
port, assign a different available port to each active instance separately.

Treat generated data here as disposable: Git-ignored files are not a backup and
can disappear when a worktree is removed. Keep valuable data outside worktrees
and back it up independently. For external databases, volumes, or other resources,
record their exact names, owning task, and teardown command in the handoff.
`tspace stop` ends the terminal session; `tspace clean` removes worktrees. Neither runs
an external-resource teardown hook. Inspect ownership before deleting resources,
especially when configuration was copied from another workspace.
