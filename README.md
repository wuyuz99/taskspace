# aw

> **Worktrees are per-repository. Features aren't.**

`aw` manages named development workspaces across repositories, with optional coding-agent commands.

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

`aw` models it as:

```
task -> repos -> worktrees -> shells or optional agents
```

The **task** is the first-class object. You operate on `checkout-redesign`, not
on nine separate resources.

Every task has a window at its root, the directory holding every worktree: run
one agent there and it works across all the repositories, while a shell in each
repository is on hand to review and commit. Agents can also run in selected
repositories. `aw` prepares and resumes the workspace; it does not coordinate
agents or schedule their work.

```
checkout-redesign
├── (root)                               claude   running
├── backend    agent/checkout-redesign   shell    running   dirty (3 files)
├── frontend   agent/checkout-redesign   shell    running   clean, ahead 2
└── infra      agent/checkout-redesign   shell    dead      clean
```

## Requirements

- A **Unix-like OS** — Linux, macOS, or BSD. **Windows is not supported.**
- `git`
- `tmux`
- Go 1.23+ to build

Agents (Claude Code, Codex, anything else) are optional external programs. `aw`
does not implement or bundle one, and the test suite does not need any.

## Install

```bash
git clone <this repo> && cd aw
go build -o aw ./cmd/aw
sudo install -m 0755 aw /usr/local/bin/aw     # or put it anywhere on PATH
```

## Quick start

```bash
# one logical task spanning two repositories, with one agent at its root
aw task create checkout-redesign --agent claude
aw repo add checkout-redesign ~/src/backend
aw repo add checkout-redesign ~/src/frontend

# create isolated worktrees and launch the agent
aw start checkout-redesign

# or prepare, show task context and enter the session in one command
aw open checkout-redesign

# one tmux session: the agent's _root window first, then a shell per repo
aw attach checkout-redesign        # detach with Ctrl-b d

# what is going on, across every task
aw status

# poke at one repo by hand without leaving your workflow
aw shell checkout-redesign backend
cd "$(aw path checkout-redesign frontend)"

# do the same thing in every worktree
aw exec checkout-redesign -- git push

# stop the agents; nothing on disk is touched
aw stop checkout-redesign

# tear the worktrees down, safely
aw clean checkout-redesign
```

### Set your agent once

If you use the same agent for every task, make it the default:

```bash
aw config agent claude --model opus   # everything after the command is its argv
aw task create checkout-redesign      # root agent: claude --model opus
aw config                             # show defaults and where they are stored
aw config agent --unset
```

A new task's root agent comes from, in order: `task create --agent` or `--shell`,
then the `--project` recipe's agent, then this default, and otherwise your
shell. The result is recorded on the task, as recipes are, so changing the
default later leaves existing tasks alone; use `aw task update TASK --agent` for
those. The default is stored in `$AW_ROOT/config.json`. It sets only the
task-root agent; repository windows remain shells unless given their own
`--agent`.

### Run an agent per repository

A repository can run its own agent instead of a shell:

```bash
aw repo add checkout-redesign ~/src/backend  --agent claude
aw repo add checkout-redesign ~/src/frontend --agent codex
```

The `_root` window is still created, running a shell unless the task has an
agent of its own.

### Work with ordinary shells

For a manual workflow without persistent terminals or agent commands:

```bash
aw task create checkout-review --repo "backend=$HOME/src/backend" --repo "frontend=$HOME/src/frontend"
aw start checkout-review --no-runtime
aw shell checkout-review
# a task with several repositories opens at its root: backend/ and frontend/
# are available in this shell; exit to return
```

## Reusable projects

Save a working task's repository configuration once, then reuse it for new tickets:

```bash
aw project save payments --from PAY-142
aw project list
aw project show payments
aw task create PAY-156 --project payments --title "Add payment retry"
aw open PAY-156
```

Recipes live in `$AW_ROOT/projects/<name>.json`. They copy the task-root agent,
repository names and local source paths, base refs, agent argv, and setup hooks.
`task create --project payments --agent codex` (or `--shell`) replaces the
recipe's root agent for the new task. Worktree paths and
branches are generated for the new task (`agent/PAY-156`); source task titles,
tickets, briefs, and handoffs are not copied. No worktrees or agents are created
until `start` or `open`.

Each task snapshots the recipe. To update the recipe for future tasks, run
`aw project save payments --from TASK --replace`; existing tasks remain unchanged.
Saving over an existing recipe without `--replace` is refused. Use `project show`
to inspect commands and hooks before applying a recipe. Saving never executes them.

`task create --project payments --repo infra=/path/to/infra` can add repositories
to the snapshot. Duplicate repository names or paths are rejected; they do not
silently override recipe entries. Validation failures leave no partial task record.
Use `task update --agent` or `repo update` to customize an inherited agent or hook. These recipes reference local checkouts; they
do not clone repositories or synchronize configuration across machines. Recorded
base refs are reused; no fetch is performed. An empty base is resolved at creation.

### Update repository settings

```bash
aw repo update PAY-142 backend --agent codex
aw repo update PAY-142 backend --post-create 'pnpm install'
aw repo update PAY-142 backend --shell
aw repo update PAY-142 backend --post-create=''
aw task update PAY-142 --agent claude --agent=--model --agent opus
aw task update PAY-142 --shell
aw project save payments --from PAY-142 --replace
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
aw task create PAY-142 --title "Handle expired cards during checkout"
aw task brief PAY-142 "Reject expired cards and show an inline error."
aw task handoff PAY-142 "Backend validation done; connect the frontend next."
aw task list
aw task show PAY-142
```

Keep requirements and acceptance criteria in the **brief**, and local progress,
next steps, validation commands, and open questions in the **handoff**. Handoff
updates are timestamped in UTC; summaries are written by you or your agent,
not inferred from Git or process activity. `task list` shows the title and the
first non-heading line of the handoff, plus its timestamp. `attach` and `shell`
print the full context before handing over the terminal.

Both are ordinary Markdown files in the task's shared context directory:

```
$AW_ROOT/worktrees/PAY-142/
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

`aw shell` and `post_create` hooks also get `AW_CONTEXT_DIR`. Nothing stops you
editing, grepping, or diffing these files directly; `aw` only reads and writes
the same two paths.

For longer Markdown notes, use your editor and import/export files:

```bash
aw task brief PAY-142 --file brief.md
aw task handoff PAY-142 --file handoff.md
aw task brief PAY-142 > brief-copy.md
aw task handoff PAY-142 --clear
aw task brief PAY-142 --edit
aw task handoff PAY-142 --edit
aw task update PAY-142 --title "Validate expired payment cards"
```

Notes survive `aw clean` — worktrees are disposable, the brief describing them
is not — and are deleted with `aw task rm`. `aw task rm --force` leaves the
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
$AW_ROOT/worktrees/checkout-redesign/
├── backend/
└── frontend/
```

Cross-repo relative paths (`../frontend`) keep working inside a task.

**Session** — one tmux session per task (`aw@<encoded-task>`). Its first window,
`_root`, opens in the task root and runs the task's agent, or your shell if it
has none. Then comes one window per repo, named after the repo. No repository
can be called `_root`: repository names start with a letter or digit. Closing
`aw` never stops an agent.

### Enter the whole workspace

`aw open TASK` prepares missing worktrees and session windows, prints the task
brief and handoff, and attaches to the session (or switches clients inside tmux).
Existing windows are preserved. Dead windows get an explicit `aw restart` hint;
opening does not restart them automatically. Setup hooks run only for newly
created worktrees, as with `aw start`. Add at least one repository before opening.

`aw start TASK` and `aw open TASK` create the `_root` window along with the
repository windows. `aw start TASK REPO...` starts only the named repositories.
A session started before root windows existed gains one, as its last window,
on the next full `start` or `open`.

```bash
aw shell PAY-142
cd "$(aw path PAY-142)"
```

The root contains the task's repository directories, useful for cross-repository
inspection or launching your preferred agent there. Without a repository argument,
`shell` and `path` use the root, unless the task has exactly one repository, in
which case they use its worktree. `--root` asks for the root explicitly and cannot
be combined with a repository. `shell` creates the root if necessary, even before
repositories are added, but does not create worktrees or start a session. `path`
only prints it. Shells expose `AW_TASK_DIR`, `AW_CONTEXT_DIR`, `AW_TASK`,
`AW_WORKTREE` (the current directory), and `AW_REPO` (empty at the task root).
The root shell has a task-only terminal label.

### Recover a stopped agent

```bash
aw restart PAY-142 backend
aw restart PAY-142 --root
```

Restart relaunches the configured command only when every pane in that window
is dead. A live agent or ad-hoc split causes refusal. Missing sessions or
windows should be created with `aw start`. Restart replaces dead-window output,
so inspect it first; it does not resume agent conversation history or rerun setup.
Ordinary `start` continues to preserve existing windows, including dead ones.

### Runtime compatibility

Persistent sessions currently require tmux. Session lifecycle and window identity
are behind `internal/runtime.Runtime`, so additional multiplexers can implement
that interface. No other backend or runtime selector is implemented yet.
Notes, task metadata, paths, plain shells, and `start --no-runtime` do not need tmux.

Session names now use the `aw@` namespace; dots are encoded as `~2e`, with the
escape marker itself escaped. `a.b` and `a-b` have distinct sessions and windows.
Window targets are exact matches, and automatic renaming is disabled.

**Upgrading from old sessions:** old `aw-...` sessions are not adopted or stopped
by this version. Start/restart refuse if a possible legacy session exists. Inspect
that session using tmux, then stop or rename it manually before starting a new
session. Old names were ambiguous, so automatic migration could affect another
task. Worktree paths, branches, and task records do not change.

## Where things live

```
$AW_ROOT/                          default ~/.aw, override with AW_ROOT
├── config.json                    user-wide defaults (the default root agent)
├── tasks/<name>.json              task identity and repository configuration
├── projects/<name>.json           reusable repository recipes
└── worktrees/<task>/
    ├── .context/TASK.md           the brief, readable from any worktree
    ├── .context/HANDOFF.md        the handoff
    └── <repo>/                    the worktrees
```

Only declarative metadata is stored. **Runtime state is never written down** —
whether a session is up and whether a worktree is dirty are asked of tmux and
git every time. Nothing can go stale, and there is no repair command.

Records, recipes and notes are written `0600`: your notes are your own material,
and not something to publish to every account on the machine. A file that cannot
be read or parsed is reported on stderr and skipped, so one damaged record never
hides the rest of your workspaces from `aw task list` or `aw status`.

`AW_ROOT` exists because you probably already keep workspace directories
somewhere visible, and a hidden `~/.aw` should not be forced on that habit.

## Fresh worktrees are empty: `post_create`

A new worktree has your tracked files and nothing else. No `.env`, no
`node_modules`, no `venv`, no local certificates. This surprises everyone once.

```bash
aw repo add checkout-redesign ~/src/backend \
  --post-create 'cp "$AW_REPO_PATH/.env" . && pnpm install'
```

The hook runs once, in the new worktree, the first time it is created, with
these variables set:

| Variable | Value |
| --- | --- |
| `AW_REPO_PATH` | the source repository the worktree came from |
| `AW_WORKTREE` | the new worktree (also the working directory) |
| `AW_TASK_DIR` | the task's workspace directory, parent of every repo |
| `AW_CONTEXT_DIR` | the task's `.context/` directory, holding its notes |
| `AW_TASK`, `AW_REPO` | the task and repo names |

Use `$AW_REPO_PATH` rather than a relative path like `../../../src/backend`.
How many `..` you need depends on where `AW_ROOT` is, so a relative hook breaks
for anyone who moves their state directory.

If the hook fails you get a warning and a worktree you can inspect — `aw` will
not delete your worktree because a provisioning step failed.

### Application and service isolation

Worktrees separate checked-out source files. They share the source repository's
Git object database, refs, and stash stack. Applications in different tasks can
still use the same listening port, database, container name, or external account.
`aw` does not allocate these resources or rewrite application configuration.
Copying a source `.env` also copies its service destinations.

For an application that reads `DATABASE_PATH` and `UPLOAD_DIR` from `.env`, a
setup hook can put its SQLite database and uploads inside its own worktree:

```bash
aw repo add checkout-review ~/src/backend --post-create '
  mkdir -p .local/uploads
  printf "DATABASE_PATH=.local/app.sqlite\nUPLOAD_DIR=.local/uploads\nWORKSPACE_LABEL=%s/%s\n" "$AW_TASK" "$AW_REPO" > .env
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
`aw stop` ends the terminal session; `aw clean` removes worktrees. Neither runs
an external-resource teardown hook. Inspect ownership before deleting resources,
especially when configuration was copied from another workspace.

## Status

`aw status` reports two **independent** things per repository:

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
  ahead/behind counts

They are separate columns because a worktree can be dirty *and* running at the
same time — that is the normal state of an agent doing its job. `aw` reports
only what it can observe; it will not claim an agent is "waiting for input",
because tmux cannot tell it that.

## Safety

The rule is: **refusing beats losing code.**

`aw clean` refuses outright if any worktree has uncommitted changes, untracked
files, a stash on its branch, or a lock. It tells you which files, in which
repo, and how to proceed.

Branches are **never deleted by default**. With `--delete-branch`, a branch is
removed only when its work is *merged into the base branch* or *pushed to an
upstream*. Both halves matter: without the upstream test a pushed-but-unmerged
branch looks unique forever, and without the merge test a merged local-only
branch could never be cleaned.

`aw clean` and `aw task rm` are deliberately different. `clean` tears down
worktrees and keeps the record; `task rm` deletes the record and refuses while
worktrees exist.

## Commands

| Command | What it does |
| --- | --- |
| `aw init` | Create/report state directories (never required) |
| `aw config` / `aw config agent [CMD ARGS... / --unset]` | Show defaults; set the default task-root agent |
| `aw project save NAME --from TASK [--replace]` | Save a reusable repository recipe |
| `aw project list` / `aw project show NAME` | Inspect saved recipes |
| `aw task create NAME [--repo name=path] [--agent CMD / --shell]` | Create a task, optionally with an agent at its root |
| `aw task update NAME [--agent CMD / --shell] [--title ...]` | Update a task's root agent and details |
| `aw task list` | List tasks |
| `aw task show NAME` | Show a task's repos in detail |
| `aw task rm NAME` | Delete the record; refuses while worktrees exist |
| `aw repo add TASK PATH [--name --branch --base --agent --post-create]` | Attach a repository |
| `aw repo rm TASK REPO` | Detach a repository (leaves the worktree) |
| `aw repo update TASK REPO [--agent CMD / --shell] [--post-create SH]` | Update saved agent and setup settings |
| `aw start TASK [REPO...]` | Create worktrees, launch the session |
| `aw open TASK` | Prepare worktrees, show brief/handoff, and attach |
| `aw restart TASK REPO` / `aw restart TASK --root` | Relaunch a dead repository or root window |
| `aw stop TASK` | Interrupt agents, end the session, keep worktrees |
| `aw attach TASK` | Attach (or `switch-client` when already in tmux) |
| `aw shell TASK [REPO / --root]` | Interactive shell in a worktree or the task root |
| `aw path TASK [REPO / --root]` | Print a worktree or task-root path, for `cd "$(...)"` |
| `aw exec TASK -- CMD` | Run a command in every worktree |
| `aw status [TASK]` | Runtime and git state |
| `aw clean TASK [--delete-branch] [--force]` | Remove worktrees, safely |

`--no-title` (or `AW_NO_TITLE=1`) suppresses terminal tab titling.

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
go build -o aw ./cmd/aw
scripts/e2e.sh ./aw
```

To try it by hand, `scripts/demo.sh` builds a two-repo playground rigged to fail
the way real projects do — the backend needs a gitignored `.env`, and the
frontend reads `../backend/api.json`, so it only works if the repos are
siblings. It prints a walkthrough, and `scripts/demo.sh --clean` removes it.

**Developing on Windows** (the tool is Unix-only, but you can build it there):
cross-compile and run under WSL.

```bash
GOOS=linux GOARCH=amd64 go build -o aw-linux ./cmd/aw
# then run scripts/e2e.sh against aw-linux inside WSL
```

## Current limitations

- **Unix only.** tmux is required; there is no Windows path.
- **Submodules** are detected and reported, but not recursed into.
- **No notifications.** `internal/status` is the single place one would attach.
- **`aw shell` opens a plain subshell**, not a tmux window in the task session.
  Predictable from any context, but it does not join the session, and it is a
  known deviation from the original design note — see `docs/REVIEW_BACKLOG.md`.
- **Briefs and handoffs render with control characters stripped.** The stored
  Markdown keeps every byte; what reaches the terminal cannot move the cursor or
  retitle the tab, because a brief is usually pasted in from somewhere else.
- **Stash attribution is a heuristic.** git keeps one stash stack per
  repository, so stashes are matched by the branch recorded in their message.
  It errs toward reporting a stash, which is the safe direction.
- **`.context/` holds two files.** `TASK.md` and `HANDOFF.md` exist; the
  `API_CONTRACT.md` and `STATUS.json` of the original sketch do not, and
  nothing writes the directory on your behalf.
- **Little configuration.** `AW_ROOT` chooses the state directory, and
  `$AW_ROOT/config.json` holds the default task-root agent. Nothing else is
  configurable.
- Existing sessions from the old `aw-...` naming scheme require manual inspection
  and stopping or renaming before starting their replacement.

## Roadmap

These are possible follow-ups after real workspace usage establishes a need;
agent coordination is not part of the current workspace workflow.

- Richer cross-repo task context beyond the brief and handoff — a shared
  API contract or status file agents write to, so they can coordinate without
  sharing a context window
- Richer agent status (`working`, `waiting_for_input`, `blocked`) via adapters
  that can actually determine it
- Agent-to-agent dependencies — "frontend waits for the backend API contract"
- `aw run TASK "prompt"` to coordinate agents across repositories
- Notifications when an agent finishes or blocks
- GitHub/PR integration: PRs, CI status, merge readiness
- More runtimes — zellij, plain subprocess, remote
- A TUI

## License

Not yet chosen.
