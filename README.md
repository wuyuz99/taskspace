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
three branches, opening three terminals, starting an agent in each, and
remembering which tab is which.

Most tools model this as:

```
repo -> worktree -> agent
```

`aw` models it as:

```
task -> repos -> worktrees -> agents
```

The **task** is the first-class object. You operate on `checkout-redesign`, not
on nine separate resources.

```
checkout-redesign
├── backend    agent/checkout-redesign   claude   running   dirty (3 files)
├── frontend   agent/checkout-redesign   codex    running   clean, ahead 2
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
# one logical task spanning two repositories
aw task create checkout-redesign
aw repo add checkout-redesign ~/src/backend  --agent claude
aw repo add checkout-redesign ~/src/frontend --agent codex

# create isolated worktrees and launch the agents
aw start checkout-redesign

# or prepare, show task context and enter the session in one command
aw open checkout-redesign

# one tmux session, one named window per repo
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

## Reusable projects

Save a working task's repository configuration once, then reuse it for new tickets:

```bash
aw project save payments --from PAY-142
aw project list
aw project show payments
aw task create PAY-156 --project payments --title "Add payment retry"
aw open PAY-156
```

Recipes live in `$AW_ROOT/projects/<name>.json`. They copy repository names and
local source paths, base refs, agent argv, and setup hooks. Worktree paths and
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
To customize an inherited entry before starting, remove it with `repo rm` and
re-add it with the desired flags. These recipes reference local checkouts; they
do not clone repositories or synchronize configuration across machines. Recorded
base refs are reused; no fetch is performed. An empty base is resolved at creation.

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
first non-heading line of the handoff (up to 90 characters), plus its timestamp.
`attach` and `shell` print the full context before handing over the terminal.

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

Notes are stored in the task JSON record, survive `aw clean`, and are deleted
with `aw task rm`. Imported files are copied into the record, not linked or
automatically synchronized. Updating a handoff never changes the brief.

`--edit` opens a temporary Markdown draft using `VISUAL`, then `EDITOR`, falling
back to `vi`. Editor commands can include arguments (for example, `code --wait`);
GUI editors must wait until editing finishes. It cannot be combined with text,
`--file`, or `--clear`. Exiting without changes leaves the note and timestamp
unchanged. Editor/save failures retain the draft and report its path. If the note
changed during editing, the command refuses to overwrite it and keeps your draft
for reconciliation. This checks changes made during the editor session; it does
not provide general locking for simultaneous task writes.

Optional `--ticket PAY-142` and `--ticket-url https://example.com/browse/PAY-142`
flags on `task create` and `task update` record ticket metadata locally.
Multiple differently named workspaces can reference the same ticket. Empty
values on `task update` clear those fields. `--brief-file` is also available
on create/update. Jira discovery, synchronization, and automatic updates are
not implemented.

## Workspace structure

**Task** — a logical unit of work. The thing you actually think in. It has a
name, and it owns one entry per repository.

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

**Session** — one tmux session per task (`aw@<encoded-task>`), one window per repo,
named after the repo. Closing `aw` never stops an agent.

### Enter the whole workspace

`aw open TASK` prepares missing worktrees and session windows, prints the task
brief and handoff, and attaches to the session (or switches clients inside tmux).
Existing windows are preserved. Dead windows get an explicit `aw restart` hint;
opening does not restart them automatically. Setup hooks run only for newly
created worktrees, as with `aw start`. Add at least one repository before opening.

```bash
aw shell PAY-142 --root
cd "$(aw path PAY-142 --root)"
```

The root contains the task's repository directories, useful for cross-repository
inspection or launching your preferred agent there. `shell --root` creates the
directory if necessary, even before repositories are added, but does not create
worktrees or start a session. `path --root` only prints its path. Neither accepts
a repository argument. Existing repository-specific shell/path commands still work.
Shells expose `AW_TASK_DIR`, `AW_TASK`, `AW_WORKTREE` (the current directory), and
`AW_REPO` (empty at the task root). The root shell has a task-only terminal label.

### Recover a stopped agent

```bash
aw restart PAY-142 backend
```

Restart relaunches the configured command only when every pane in that repository
window is dead. A live agent or ad-hoc split causes refusal. Missing sessions or
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
$AW_ROOT/                    default ~/.aw, override with AW_ROOT
├── tasks/<name>.json        declarative task metadata
└── worktrees/<task>/<repo>  the worktrees
```

Only declarative metadata is stored. **Runtime state is never written down** —
whether a session is up and whether a worktree is dirty are asked of tmux and
git every time. Nothing can go stale, and there is no repair command.

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
| `AW_TASK`, `AW_REPO` | the task and repo names |

Use `$AW_REPO_PATH` rather than a relative path like `../../../src/backend`.
How many `..` you need depends on where `AW_ROOT` is, so a relative hook breaks
for anyone who moves their state directory.

If the hook fails you get a warning and a worktree you can inspect — `aw` will
not delete your worktree because a provisioning step failed.

## Status

`aw status` reports two **independent** things per repository:

```
TASK               REPO      BRANCH                   AGENT   RUNTIME     GIT
checkout-redesign  backend   agent/checkout-redesign  claude  running     dirty (3 files)
checkout-redesign  frontend  agent/checkout-redesign  codex   dead        clean, ahead 2
payments-fix       backend   agent/payments-fix       codex   no-session  missing
```

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
| `aw project save NAME --from TASK [--replace]` | Save a reusable repository recipe |
| `aw project list` / `aw project show NAME` | Inspect saved recipes |
| `aw task create NAME [--repo name=path]` | Create a task |
| `aw task list` | List tasks |
| `aw task show NAME` | Show a task's repos in detail |
| `aw task rm NAME` | Delete the record; refuses while worktrees exist |
| `aw repo add TASK PATH [--name --branch --base --agent --post-create]` | Attach a repository |
| `aw repo rm TASK REPO` | Detach a repository (leaves the worktree) |
| `aw start TASK [REPO...]` | Create worktrees, launch the session |
| `aw open TASK` | Prepare worktrees, show brief/handoff, and attach |
| `aw restart TASK REPO` | Relaunch a dead repository window |
| `aw stop TASK` | Interrupt agents, end the session, keep worktrees |
| `aw attach TASK` | Attach (or `switch-client` when already in tmux) |
| `aw shell TASK [REPO]` | Interactive shell in a worktree |
| `aw path TASK [REPO]` | Print a worktree path, for `cd "$(...)"` |
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
  Predictable from any context, but it does not join the session.
- **Stash attribution is a heuristic.** git keeps one stash stack per
  repository, so stashes are matched by the branch recorded in their message.
  It errs toward reporting a stash, which is the safe direction.
- **No `.context/`** shared task directory yet; the location is reserved.
- **One config knob** (`AW_ROOT`). There is no config file.
- Existing sessions from the old `aw-...` naming scheme require manual inspection
  and stopping or renaming before starting their replacement.

## Roadmap

Deliberately not built yet:

- Cross-repo task context (`.context/TASK.md`, `API_CONTRACT.md`, …) so agents
  can coordinate without sharing a context window
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
