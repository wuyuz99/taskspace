# tspace

> **Worktrees are per-repository. Features aren't.**

`tspace` manages named development workspaces across repositories, with optional coding-agent commands.

> **Early-stage project.** v0.1, Unix-only, and the command surface will change.
> It is usable on real repositories today — that was the point of the first
> milestone — but treat it as something you can read end to end in an afternoon,
> not as settled software.

---

## The problem

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

Every task has a window at its root, the directory holding every worktree: run
one agent there and it works across all the repositories, while a shell in each
repository is on hand to review and commit. Agents can also run in selected
repositories. `tspace` prepares and resumes the workspace; it does not coordinate
agents or schedule their work.

```
checkout-redesign
├── (root)                               claude   running
├── backend    agent/checkout-redesign   shell    running   dirty (3 files)
├── frontend   agent/checkout-redesign   shell    running   clean, ahead 2
└── infra      agent/checkout-redesign   shell    dead      clean
```

## Requirements

- A **Unix-like OS** — Linux or macOS (BSD likely works, untested). **Windows
  is not supported**; use WSL.
- `git`
- `tmux` (on macOS: `brew install tmux`)
- Go 1.26+ to build

Agents (Claude Code, Codex, anything else) are optional external programs. `tspace`
does not implement or bundle one, and the test suite does not need any.

## Install

With Go 1.26 or later:

```bash
go install github.com/wuyuz99/taskspace/cmd/tspace@latest
```

This puts `tspace` in `$(go env GOPATH)/bin`, usually `~/go/bin`; make sure
that is on your `PATH`.

Without Go, download a binary for Linux or macOS (amd64 or arm64) from the
[releases page](https://github.com/wuyuz99/taskspace/releases):

```bash
tar -xzf tspace_v0.1.0_linux_amd64.tar.gz
sudo install -m 0755 tspace_v0.1.0_linux_amd64/tspace /usr/local/bin/tspace   # or anywhere on PATH
tspace --version
```

From source:

```bash
git clone https://github.com/wuyuz99/taskspace.git && cd taskspace
go build -o tspace ./cmd/tspace
sudo install -m 0755 tspace /usr/local/bin/tspace
```

## Quick start

```bash
# once: your agent, and the repositories you work in
tspace config agent claude
tspace config repo backend  ~/src/backend
tspace config repo frontend ~/src/frontend
tspace config repo infra    ~/src/infra

# one logical task spanning the repositories it needs: a new branch in
# backend, a colleague's existing branch in frontend, and a new branch in
# infra forked from develop
tspace task create checkout-redesign backend frontend@feature/cart infra:develop

# create the worktrees, show the task context and enter its session
tspace open checkout-redesign

# one tmux session: the agent's _root window first, then a shell per repo
tspace attach checkout-redesign        # detach with Ctrl-b d

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
```

Repositories can also be given as paths (`tspace task create T ~/src/backend`), and
added later with `tspace repo add TASK REPO`.

### Choose branches

Each repository in a task is written `REPO[@BRANCH][:BASE]`, where `REPO` is a
name registered with `tspace config repo` or a path:

| You write | Branch | Created from |
| --- | --- | --- |
| `backend` | `agent/TASK` | `origin/HEAD` (else `main`/`master`) |
| `backend:develop` | `agent/TASK` | `develop`, or `origin/develop` if there is no local one |
| `backend@feature/x` | `feature/x` | an existing branch is used as it is |
| `backend@fix-12:release/2.1` | `fix-12` | `release/2.1` |
| `api=~/src/backend@x` | `x`, in a repo named `api` | |

A named branch that exists locally is checked out as it is. One that exists
only on a remote — a colleague's, or your own from another machine — is
checked out as a local branch tracking it. Only when neither exists is it
created from the base. `repo add` says which of the three will happen, and
fetches first when you named a branch or base the clone has not seen.
`start` fetches again just before creating a branch, so a branch pushed in
between is still found; if the fetch fails you get a warning and a worktree
made from the refs you already have.

New branches start **with no upstream**. git would otherwise make
`origin/main` the upstream of a branch forked from it, and then `git push`
refuses — or, with `push.default=upstream`, pushes the task's work to main.
Push with `git push -u origin HEAD`. Until then `tspace status` counts the commits
that are not on the base as "not pushed".

The `agent/` prefix is configurable. Branches are recorded when a repository is
added, so changing it leaves existing tasks alone:

```bash
tspace config branch-prefix me/        # me/PAY-142
tspace config branch-prefix ''         # PAY-142
tspace config branch-prefix --unset    # back to agent/PAY-142
```

### Set your agent once

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
task-root agent; repository windows remain shells unless given their own
`--agent`.

### Open more windows as you go

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
still be read. Inside tmux you are switched to the new window. Extra windows are
not part of the task record: `start` and `open` do not recreate them, and `stop`
ends them with the rest of the session.

`tspace shell` is the other way to get a terminal: a plain subshell in the terminal
you are already in, labelled and with `TSPACE_*` variables set, whether or not the
session is running.

### Run an agent per repository

A repository can run its own agent instead of a shell:

```bash
tspace repo add checkout-redesign ~/src/backend  --agent claude
tspace repo add checkout-redesign ~/src/frontend --agent codex
```

The `_root` window is still created, running a shell unless the task has an
agent of its own. For an agent in a repository only now and then, `tspace window
TASK REPO --agent` keeps the repository's shell window as it is.

### Work with ordinary shells

For a manual workflow without persistent terminals or agent commands:

```bash
tspace task create checkout-review "backend=$HOME/src/backend" "frontend=$HOME/src/frontend"
tspace start checkout-review --no-runtime
tspace shell checkout-review
# a task with several repositories opens at its root: backend/ and frontend/
# are available in this shell; exit to return
```

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
save it back explicitly when desired. Repository paths, branches, and base refs
are not changed by `repo update`.

## Human-readable task context

A workspace can be named after a ticket, or use any ordinary task name.
No ticket key, URL, Jira connection, or account is required.

```bash
tspace task create PAY-142 --title "Handle expired cards during checkout"
tspace task brief PAY-142 "Reject expired cards and show an inline error."
tspace task handoff PAY-142 "Backend validation done; connect the frontend next."
tspace task list
tspace task show PAY-142
```

Keep requirements and acceptance criteria in the **brief**, and local progress,
next steps, validation commands, and open questions in the **handoff**. Handoff
updates are timestamped in UTC; summaries are written by you or your agent,
not inferred from Git or process activity. `task list` shows the title and the
first non-heading line of the handoff, plus its timestamp. `attach` and `shell`
print the full context before handing over the terminal.

Both are ordinary Markdown files in the task's shared context directory:

```
$TSPACE_ROOT/worktrees/PAY-142/
├── .context/
│   ├── TASK.md       the brief
│   └── HANDOFF.md    the handoff
├── backend/          worktree
└── frontend/         worktree
```

Because `.context/` is a sibling of the worktrees, anything running in one
reaches it with a relative path — which is the point. An agent working in
`backend/` can read the task's own requirements:

```bash
cat ../.context/TASK.md
```

The agent in the `_root` window starts one level up, so for it the same file is
`.context/TASK.md`.

`tspace shell` and `post_create` hooks also get `TSPACE_CONTEXT_DIR`. Nothing stops you
editing, grepping, or diffing these files directly; `tspace` only reads and writes
the same two paths.

For longer Markdown notes, use your editor and import/export files:

```bash
tspace task brief PAY-142 --file brief.md
tspace task handoff PAY-142 --file handoff.md
tspace task brief PAY-142 > brief-copy.md
tspace task handoff PAY-142 --clear
tspace task brief PAY-142 --edit
tspace task handoff PAY-142 --edit
tspace task update PAY-142 --title "Validate expired payment cards"
```

Notes survive `tspace clean` — worktrees are disposable, the brief describing them
is not — and are deleted with `tspace task rm`. `tspace task rm --force` leaves the
worktrees on disk, so it leaves the notes too and tells you where they are.
`--file` copies a file's contents in; it does not link to it or keep it in sync.
Updating a handoff never changes the brief, and clearing a note removes its file,
so it has no timestamp rather than a misleading one.

`--edit` opens the note file itself using `VISUAL`, then `EDITOR`, falling back
to `vi`. Editor commands can include arguments (for example, `code --wait`); GUI
editors must wait until editing finishes. It cannot be combined with text,
`--file`, or `--clear`. Whatever the editor saved is the note — there is no draft
to reconcile, and no copy to lose if the editor or the machine dies mid-edit.
Saving an empty file means the same as `--clear`. Two editors open on one note
is the editors' business to warn about, as with any other file.

Records written before notes were files carried the Markdown inline; they are
moved into `.context/` the first time anything reads a note, keeping the recorded
handoff time. Nothing to run, and no flag.

Optional `--ticket PAY-142` and `--ticket-url https://example.com/browse/PAY-142`
flags on `task create` and `task update` record ticket metadata locally.
Multiple differently named workspaces can reference the same ticket. Empty
values on `task update` clear those fields. `--brief-file` is also available
on create/update. Jira discovery, synchronization, and automatic updates are
not implemented.

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

**Session** — one tmux session per task (`tspace@<encoded-task>`). Its first window,
`_root`, opens in the task root and runs the task's agent, or your shell if it
has none. Then comes one window per repo, named after the repo. No repository
can be called `_root`: repository names start with a letter or digit. Closing
`tspace` never stops an agent.

### Enter the whole workspace

`tspace open TASK` prepares missing worktrees and session windows, prints the task
brief and handoff, and attaches to the session (or switches clients inside tmux).
Existing windows are preserved. Dead windows get an explicit `tspace restart` hint;
opening does not restart them automatically. Setup hooks run only for newly
created worktrees, as with `tspace start`. Add at least one repository before opening.

`tspace start TASK` and `tspace open TASK` create the `_root` window along with the
repository windows. `tspace start TASK REPO...` starts only the named repositories.
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
`TSPACE_WORKTREE` (the current directory), and `TSPACE_REPO` (empty at the task root).
The root shell has a task-only terminal label.

### Recover a stopped agent

```bash
tspace restart PAY-142 backend
tspace restart PAY-142 --root
```

Restart relaunches the configured command only when every pane in that window
is dead. A live agent or ad-hoc split causes refusal. Missing sessions or
windows should be created with `tspace start`. Restart replaces dead-window output,
so inspect it first; it does not resume agent conversation history or rerun setup.
Ordinary `start` continues to preserve existing windows, including dead ones.

### Runtime compatibility

Persistent sessions currently require tmux. Session lifecycle and window identity
are behind `internal/runtime.Runtime`, so additional multiplexers can implement
that interface. No other backend or runtime selector is implemented yet.
Notes, task metadata, paths, plain shells, and `start --no-runtime` do not need tmux.

Session names now use the `tspace@` namespace; dots are encoded as `~2e`, with the
escape marker itself escaped. `a.b` and `a-b` have distinct sessions and windows.
Window targets are exact matches, and automatic renaming is disabled.

**Upgrading from old sessions:** sessions named `aw@...` (from before the rename
to `tspace`) or `aw-...` are not adopted or stopped
by this version. Start/restart refuse if a possible legacy session exists. Inspect
that session using tmux, then stop or rename it manually before starting a new
session. Old names were ambiguous, so automatic migration could affect another
task. Worktree paths, branches, and task records do not change.

## Where things live

```
$TSPACE_ROOT/                          default ~/.tspace, override with TSPACE_ROOT
├── config.json                    user-wide defaults: root agent, registered repos, branch prefix
├── tasks/<name>.json              task identity and repository configuration
├── projects/<name>.json           reusable repository recipes
└── worktrees/<task>/
    ├── .context/TASK.md           the brief, readable from any worktree
    ├── .context/HANDOFF.md        the handoff
    ├── .context/signals/          what each window's agent last reported
    └── <repo>/                    the worktrees
```

Only declarative metadata is stored. **Runtime state is never written down** —
whether a session is up and whether a worktree is dirty are asked of tmux and
git every time. Nothing can go stale, and there is no repair command.

Records, recipes and notes are written `0600`: your notes are your own material,
and not something to publish to every account on the machine. A file that cannot
be read or parsed is reported on stderr and skipped, so one damaged record never
hides the rest of your workspaces from `tspace task list` or `tspace status`.

`TSPACE_ROOT` exists because you probably already keep workspace directories
somewhere visible, and a hidden `~/.tspace` should not be forced on that habit.

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

## Status

`tspace status` reports two **independent** things per repository:

```
TASK               REPO      BRANCH                   AGENT   RUNTIME     GIT
checkout-redesign  (root)    -                        claude  running     -
checkout-redesign  backend   agent/checkout-redesign  shell   running     dirty (3 files)
checkout-redesign  frontend  agent/checkout-redesign  codex   dead        clean, ahead 2
payments-fix       (root)    -                        shell   no-session  -
payments-fix       backend   agent/payments-fix       codex   no-session  missing
```

Each task's first row is its `_root` window. It has a runtime state but no git
state of its own: whatever its agent changes shows up in the repositories' rows.

- **RUNTIME** — `running`, `dead`, `no-window`, `no-session`, `unknown`
- **GIT** — `clean`, `dirty (N files)`, `missing`, `error`, plus stash and
  ahead/behind counts against the upstream, or for a branch not yet pushed,
  the commits that are not on its base (`2 commits not pushed`)

They are separate columns because a worktree can be dirty *and* running at the
same time — that is the normal state of an agent doing its job. `tspace` reports
only what it can observe; it will not guess that an agent is "waiting for
input", because tmux cannot tell it that.

`tspace status --watch` redraws the table every two seconds (`--interval`), and
`--json` prints the same observations for a script.

### Signals

Only the agent knows whether it is busy, waiting on you, or finished, so it
says so itself:

```bash
tspace signal blocked 'totals endpoint: round per line or per order?'
tspace signal done
```

The states are `working`, `blocked`, `done` and `failed`. Run inside a task,
`tspace signal` works out which window it is from the current directory, so it
needs no arguments in an agent's hooks. For Claude Code, in the worktree's
`.claude/settings.json`:

```json
{
  "hooks": {
    "UserPromptSubmit": [{ "hooks": [{ "type": "command", "command": "tspace signal working" }] }],
    "Notification":     [{ "hooks": [{ "type": "command", "command": "tspace signal blocked" }] }],
    "Stop":             [{ "hooks": [{ "type": "command", "command": "tspace signal done" }] }]
  }
}
```

Once any window has a signal, `tspace status` grows a `SIGNAL` column (`blocked 3m`),
and `--json` carries the state, message and time. A signal is the agent's word,
shown beside `RUNTIME` and never instead of it. It is one file,
`.context/signals/REPO.json` (`_root.json` for the root window), so an agent
without `tspace` on its `PATH` can write `{"state": "blocked", "message": "..."}`
there directly. `tspace stop`, `tspace clean` and a fresh `tspace start` clear a task's
signals; `tspace restart` clears the window's own.

### Dashboard

`tspace dash` is the same information, live and full-screen, for supervising
several agents at once:

```
tspace dash  2 tasks · 1 blocked · 1 dead  22:53:17

  TASK               REPO      AGENT   RUNTIME     SIGNAL       GIT
  checkout-redesign  (root)    claude  running     working 40s  -
                     backend   claude  running     done 12m     dirty (3 files)
>                    frontend  codex   running     blocked 3m   clean, ahead 2
  payments-fix       (root)    shell   no-session  -            -
                     backend   codex   dead        -            missing

checkout-redesign/frontend  agent/checkout-redesign
  /home/me/.tspace/worktrees/checkout-redesign/frontend
  signal   blocked, 3m ago: totals endpoint: round per line or per order?
  handoff  API done, tests pass.

enter attach · s start · r restart · x stop · c clear signal · q quit
```

`enter` attaches to the selected window and detaching brings you back; inside
tmux it switches the client instead and the dashboard keeps running in its own
window. Every key runs the command you would have typed.

## Safety

The rule is: **refusing beats losing code.**

`tspace clean` refuses outright if any worktree has uncommitted changes, untracked
files, a stash on its branch, or a lock. It tells you which files, in which
repo, and how to proceed.

For a detached HEAD, cleanup also verifies that its commits are reachable from
a surviving branch, tag, or remote-tracking ref. Otherwise it refuses: create a
branch at HEAD to preserve the work before cleaning. `--force` overrides this
protection, as it does for uncommitted changes.

Branches are **never deleted by default**. With `--delete-branch`, a branch is
removed only when its work is *merged into the base branch* or *pushed to an
upstream*. Both halves matter: without the upstream test a pushed-but-unmerged
branch looks unique forever, and without the merge test a merged local-only
branch could never be cleaned.

The reference protecting that work must survive deletion. A branch used as its
own base or upstream, including through a symbolic alias, is not protection by
itself; cleanup keeps the branch unless another surviving reference protects it.

A **squash merge** counts as merged: none of the branch's commits reach the
base, and the hosting service has often deleted the remote branch too, so
`clean` also looks for a single commit on the base that makes exactly the
branch's combined change. A squash that needed conflict resolution changes
that patch and is not recognised, so the branch is kept. The base is compared
as your clone last fetched it; `git fetch` first if the merge just happened.

`tspace clean` and `tspace task rm` are deliberately different. `clean` tears down
worktrees and keeps the record; `task rm` deletes the record and refuses while
worktrees exist.

## Commands

| Command | What it does |
| --- | --- |
| `tspace init` | Create/report state directories (never required) |
| `tspace config` / `tspace config agent [CMD ARGS... / --unset]` | Show defaults; set the default task-root agent |
| `tspace config repo [NAME [PATH] / NAME --unset]` | Register source repositories by name |
| `tspace config branch-prefix [PREFIX / --unset]` | Set the prefix of new task branches (default `agent/`) |
| `tspace project save NAME --from TASK [--replace]` | Save a reusable repository recipe |
| `tspace project list` / `tspace project show NAME` | Inspect saved recipes |
| `tspace task create NAME [REPO...] [--agent CMD / --shell]` | Create a task; each REPO is `REPO[@BRANCH][:BASE]` |
| `tspace task update NAME [--agent CMD / --shell] [--title ...]` | Update a task's root agent and details |
| `tspace task list [--json]` | List tasks |
| `tspace task show NAME [--json]` | Show a task's repos in detail |
| `tspace task rm NAME` | Delete the record; refuses while worktrees exist |
| `tspace repo add TASK REPO [--name --branch --base --agent --post-create]` | Attach a repository, by registered name or path |
| `tspace repo rm TASK REPO` | Detach a repository (leaves the worktree) |
| `tspace repo update TASK REPO [--agent CMD / --shell] [--post-create SH]` | Update saved agent and setup settings |
| `tspace start TASK [REPO...]` | Create worktrees, launch the session |
| `tspace open TASK` | Prepare worktrees, show brief/handoff, and attach |
| `tspace restart TASK REPO` / `tspace restart TASK --root` | Relaunch a dead repository or root window |
| `tspace stop TASK` | Interrupt agents, end the session, keep worktrees |
| `tspace attach TASK` | Attach (or `switch-client` when already in tmux) |
| `tspace window TASK [REPO / --root] [--agent / -- CMD]` | Open another window in the running session |
| `tspace shell TASK [REPO / --root]` | Interactive shell in a worktree or the task root |
| `tspace path TASK [REPO / --root]` | Print a worktree or task-root path, for `cd "$(...)"` |
| `tspace exec TASK -- CMD` | Run a command in every worktree |
| `tspace status [TASK] [--json / --watch]` | Runtime, git and signal state |
| `tspace dash` | Live full-screen view of every task; attach, start, restart, stop |
| `tspace signal STATE [MESSAGE] [--task --repo / --root]` | Report an agent as `working`, `blocked`, `done` or `failed` |
| `tspace clean TASK [--delete-branch] [--force]` | Remove worktrees, safely |

`--no-title` (or `TSPACE_NO_TITLE=1`) suppresses terminal tab titling.

## Development

```bash
gofmt -l .          # must print nothing
go vet ./...
go test ./...       # unit + git integration tests
```

Git tests build real temporary repositories rather than mocking git — mocking
would only test our idea of git. tmux tests skip themselves when tmux is
absent. No agent is needed to run the suite.

The full end-to-end check, against real repos and a real tmux server:

```bash
go build -o tspace ./cmd/tspace
scripts/e2e.sh ./tspace
```

To try it by hand, `scripts/demo.sh` builds a two-repo playground rigged to fail
the way real projects do — the backend needs a gitignored `.env`, and the
frontend reads `../backend/api.json`, so it only works if the repos are
siblings. It prints a walkthrough, and `scripts/demo.sh --clean` removes it.

**Developing on Windows** (the tool is Unix-only, but you can build it there):
cross-compile and run under WSL.

```bash
GOOS=linux GOARCH=amd64 go build -o tspace-linux ./cmd/tspace
# then run scripts/e2e.sh against tspace-linux inside WSL
```

## Current limitations

- **Unix only.** tmux is required; there is no Windows path.
- **Submodules** are detected and reported, but not recursed into.
- **No notifications.** `internal/status` is the single place one would attach.
- **Extra windows are not remembered.** `tspace window` adds to the running
  session only; after `stop`, `start` recreates the root and repository
  windows, not the extras.
- **Agent windows get no `TSPACE_*` variables.** Shells from `tspace shell` and hooks
  do; tmux windows, including `tspace window`, start from tmux's environment.
- **Briefs and handoffs render with control characters stripped.** The stored
  Markdown keeps every byte; what reaches the terminal cannot move the cursor or
  retitle the tab, because a brief is usually pasted in from somewhere else.
- **Stash attribution is a heuristic.** git keeps one stash stack per
  repository, so stashes are matched by the branch recorded in their message.
  It errs toward reporting a stash, which is the safe direction.
- **`.context/` holds two notes and the signals.** `TASK.md`, `HANDOFF.md`
  and `signals/` exist; the `API_CONTRACT.md` and `STATUS.json` of the
  original sketch do not, and nothing writes notes on your behalf.
- **Little configuration.** `TSPACE_ROOT` chooses the state directory, and
  `$TSPACE_ROOT/config.json` holds the default task-root agent, the registered
  repositories and the branch prefix. Nothing else is configurable.
- Existing sessions from the old `aw@...` or `aw-...` naming schemes require manual inspection
  and stopping or renaming before starting their replacement.

## Roadmap

These are possible follow-ups after real workspace usage establishes a need;
agent coordination is not part of the current workspace workflow.

- Richer cross-repo task context beyond the brief and handoff — a shared
  API contract or status file agents write to, so they can coordinate without
  sharing a context window
- Agent status that is observed rather than reported: adapters that can tell
  an agent is waiting without it running `tspace signal`
- Agent-to-agent dependencies — "frontend waits for the backend API contract"
- `tspace run TASK "prompt"` to coordinate agents across repositories
- Notifications when an agent finishes or blocks
- GitHub/PR integration: PRs, CI status, merge readiness
- More runtimes — zellij, plain subprocess, remote

## License

MIT — see [LICENSE](LICENSE).
