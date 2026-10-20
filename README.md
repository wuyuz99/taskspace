# tspace

> **Worktrees are per-repository. Features aren't.**

<a id="the-problem"></a>

`tspace` gives a development task one workspace across several Git repositories.
It creates sibling worktrees on task branches and opens their shells in one
terminal session. Coding agents are optional external commands.

For a feature that touches both a backend and a frontend:

```bash
tspace task create checkout-redesign backend frontend
tspace open checkout-redesign
```

The task holds the repository choices, branches and shared notes. You can stop
its terminal session, return later, or remove the worktrees when finished.
`tspace` prepares and resumes workspaces; it does not coordinate agents or
schedule their work.

## Requirements

- **Linux or macOS.** Other Unix-like systems may work but are untested. Native
  Windows is unsupported; use WSL.
- **Git** and existing local repositories for the quick start below.
- **tmux** for terminal sessions (`brew install tmux` on macOS). zellij 0.44+
  and custom adapters are also supported; see [multiplexers](docs/MULTIPLEXERS.md).
- **Go 1.26+** for the recommended installation command. Prebuilt binaries are
  available if you do not use Go.

Metadata commands and `start --no-runtime` can work without a multiplexer.
No coding agent, ticket tracker or external account is required.

## Install

With Go 1.26 or later:

```bash
go install github.com/wuyuz99/taskspace/cmd/tspace@latest
```

Add `$(go env GOPATH)/bin` (usually `~/go/bin`) to your `PATH`.
For [prebuilt Linux/macOS binaries or a source build](docs/INSTALL.md), see the
installation guide.

## Quick start

Use two existing Git checkouts. Replace the example paths below with your own;
`backend` and `frontend` are the names you will use inside tspace.

```bash
tspace config repo backend  ~/src/backend
tspace config repo frontend ~/src/frontend

tspace task create checkout-redesign backend frontend --shell
tspace open checkout-redesign
```

`task create` records the task. `open` creates its worktrees, prints its brief
and handoff, and attaches to the terminal session. The explicit `--shell` keeps
this first task free of agent commands, even if you previously configured one.

Each new repository branch is `agent/checkout-redesign`, based on that
repository's default branch: `origin/HEAD`, then local `main`/`master`, and
finally `HEAD` when neither is available.
An existing task branch is reused. You can choose other branches and bases
later; see [branch selection](docs/BRANCHING.md).

The two worktrees are siblings:

```text
~/.tspace/worktrees/checkout-redesign/
├── .context/       shared brief and handoff
├── backend/        backend worktree
└── frontend/       frontend worktree
```

This task opens a shell at the task root plus a shell for each repository.
Detach with **Ctrl-b, then d** in tmux; this leaves the session running.

**Single-repository tasks** normally have only the repository window, with the
task's shell or agent running in that worktree. `--with-root` keeps a root window.
Adding a second repository preserves the existing agent's location and adds a
root shell. See [workspace layout](docs/WORKSPACE.md) for the complete rules.

## Daily workflow

Run these from a terminal outside the task's attached session:

```bash
tspace status                         # inspect all tasks
tspace task show checkout-redesign    # inspect this task's configuration
tspace open checkout-redesign         # return to the task
```

Inside the session, work and commit as you would in any Git checkout. A new
branch starts without an upstream; push it with `git push -u origin HEAD`
from its repository when ready. Shared [briefs and handoffs](docs/TASK_CONTEXT.md)
keep the task's requirements and next steps beside its worktrees.

When finished, detach and choose the action you need:

| Command | What remains |
| --- | --- |
| `tspace stop checkout-redesign` | Ends the terminal session; worktrees and task record stay |
| `tspace clean checkout-redesign` | Stops the session and removes worktrees; task record and notes stay |
| `tspace clean checkout-redesign --rm` | Removes worktrees, then deletes the task record and notes |

Cleanup refuses dirty or untracked files, stashes and locks. Branches are kept
by default. `task rm` alone deletes a record and refuses while worktrees exist.
Read [safe cleanup](docs/SAFETY.md) before using `--force` or `--delete-branch`.
Stopping or cleaning does not tear down external databases, containers or other
application resources; see [service isolation](docs/WORKFLOWS.md#application-and-service-isolation).

## Guides and reference

| Topic | Guide |
| --- | --- |
| Agents, defaults and resuming a conversation | [Optional coding agents](docs/CODING_AGENTS.md) |
| Branches, bases and upstreams | [Branch selection](docs/BRANCHING.md) |
| Extra windows, plain shells, editors, imports and setup hooks | [Working in a task](docs/WORKFLOWS.md) |
| Root windows, paths and session layout | [Workspace structure](docs/WORKSPACE.md) |
| Reusable projects, repository settings and state files | [Configuration](docs/CONFIGURATION.md) |
| Briefs, handoffs and ticket metadata | [Task context](docs/TASK_CONTEXT.md) |
| tmux, zellij and custom adapters | [Multiplexers](docs/MULTIPLEXERS.md) |
| Status, signals, dashboard and troubleshooting | [Status guide](docs/STATUS.md) |
| Cleanup protections | [Safety](docs/SAFETY.md) |
| Full command surface | [Command reference](docs/COMMANDS.md) |
| Tests, demo playground and WSL development | [Development](docs/DEVELOPMENT.md) |

## Project status and contributing

**Early-stage, v0.1, Unix-only.** The command surface can change. See
[current limitations and possible follow-ups](docs/PROJECT_STATUS.md) before
relying on a particular workflow. The [implementation plan](docs/IMPLEMENTATION_PLAN.md)
records the original design; the guides above describe shipped behaviour.

For a change, start with the [development checks](docs/DEVELOPMENT.md) and keep
it focused. [Issues](https://github.com/wuyuz99/taskspace/issues) are the place
to report a problem or discuss a proposal.

## Earlier README sections

<details>
<summary>Previous section links and their new locations</summary>

- <a id="choose-branches"></a>[Choose branches](docs/BRANCHING.md#choose-branches)
- <a id="set-your-agent-once"></a>[Set your agent once](docs/CODING_AGENTS.md#set-your-agent-once)
- <a id="run-an-agent-per-repository"></a>[Run an agent per repository](docs/CODING_AGENTS.md#run-an-agent-per-repository)
- <a id="recover-a-stopped-agent"></a>[Recover a stopped agent](docs/CODING_AGENTS.md#recover-a-stopped-agent)
- <a id="open-more-windows-as-you-go"></a>[Open more windows as you go](docs/WORKFLOWS.md#open-more-windows-as-you-go)
- <a id="work-with-ordinary-shells"></a>[Work with ordinary shells](docs/WORKFLOWS.md#work-with-ordinary-shells)
- <a id="every-worktree-in-one-editor-window-editor"></a>[Every worktree in one editor window: editor](docs/WORKFLOWS.md#every-worktree-in-one-editor-window-editor)
- <a id="adopting-a-worktree-you-already-have-import"></a>[Adopting a worktree you already have: import](docs/WORKFLOWS.md#adopting-a-worktree-you-already-have-import)
- <a id="fresh-worktrees-are-empty-post_create"></a>[Fresh worktrees are empty: post_create](docs/WORKFLOWS.md#fresh-worktrees-are-empty-post_create)
- <a id="application-and-service-isolation"></a>[Application and service isolation](docs/WORKFLOWS.md#application-and-service-isolation)
- <a id="reusable-projects"></a>[Reusable projects](docs/CONFIGURATION.md#reusable-projects)
- <a id="update-repository-settings"></a>[Update repository settings](docs/CONFIGURATION.md#update-repository-settings)
- <a id="where-things-live"></a>[Where things live](docs/CONFIGURATION.md#where-things-live)
- <a id="record-formats-and-upgrades"></a>[Record formats and upgrades](docs/CONFIGURATION.md#record-formats-and-upgrades)
- <a id="human-readable-task-context"></a>[Human-readable task context](docs/TASK_CONTEXT.md#human-readable-task-context)
- <a id="workspace-structure"></a>[Workspace structure](docs/WORKSPACE.md#workspace-structure)
- <a id="enter-the-whole-workspace"></a>[Enter the whole workspace](docs/WORKSPACE.md#enter-the-whole-workspace)
- <a id="choose-a-multiplexer"></a>[Choose a multiplexer](docs/MULTIPLEXERS.md)
- <a id="status"></a>[Status](docs/STATUS.md#status)
- <a id="signals"></a>[Signals](docs/STATUS.md#signals)
- <a id="dashboard"></a>[Dashboard](docs/STATUS.md#dashboard)
- <a id="safety"></a>[Safety](docs/SAFETY.md#safety)
- <a id="commands"></a>[Commands](docs/COMMANDS.md#commands)
- <a id="development"></a>[Development](docs/DEVELOPMENT.md#development)
- <a id="current-limitations"></a>[Current limitations](docs/PROJECT_STATUS.md#current-limitations)
- <a id="roadmap"></a>[Roadmap](docs/PROJECT_STATUS.md#roadmap)

</details>

## License

MIT - see [LICENSE](LICENSE).
