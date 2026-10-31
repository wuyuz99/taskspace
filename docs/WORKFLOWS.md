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

## Adopting a worktree you already have: `import`

A worktree you made yourself with `git worktree add` can become a task's:

```bash
tspace import ../backend-fix                   # a new task, named backend-fix
tspace import ~/wt/api-login --task PAY-142    # into an existing task
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
