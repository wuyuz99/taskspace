# tspace

[![CI](https://github.com/wuyuz99/taskspace/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/wuyuz99/taskspace/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/wuyuz99/taskspace)](https://github.com/wuyuz99/taskspace/releases/latest)
[![License: MIT](https://img.shields.io/github/license/wuyuz99/taskspace)](LICENSE)

> **Worktrees are per-repository. Features aren't.**

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

Metadata commands and `start --no-runtime` can work without a multiplexer.
No coding agent, ticket tracker or external account is required.

## Install

Download the latest release for Linux or macOS (amd64 or arm64):

```bash
os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m | sed -e s/x86_64/amd64/ -e s/aarch64/arm64/)
tmp=$(mktemp -d)
curl -fsSL "https://github.com/wuyuz99/taskspace/releases/latest/download/tspace_${os}_${arch}.tar.gz" |
  tar -xzf - -C "$tmp" --strip-components=1
sudo install -m 0755 "$tmp/tspace" /usr/local/bin/tspace   # or anywhere on PATH
tspace --version
```

With Go 1.26 or later, you can instead run:

```bash
go install github.com/wuyuz99/taskspace/cmd/tspace@latest
```

The [installation guide](docs/INSTALL.md) covers upgrades, checksums, a specific
version and source builds.

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

`tspace dash` shows every task live and runs the same commands from a key: `n`
creates a task, `enter` goes to it, `s` and `x` start and stop it, `D` cleans it
and `R` deletes it. `t` opens it in a new terminal tab, once you have told
tspace how with [`config terminal`](docs/STATUS.md#dashboard).

Inside the session, work and commit as you would in any Git checkout. A new
branch starts without an upstream; push it with `git push -u origin HEAD`
from its repository when ready. Pushing and pull requests stay with your own
tools. Shared [briefs and handoffs](docs/TASK_CONTEXT.md) keep the task's
requirements and next steps beside its worktrees.

To review a change that spans repositories, make a task from its branches.
They are checked out as they are, including ones only on the remote:

```bash
tspace task create review-checkout backend@feature/api frontend@feature/ui --shell
```

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
| Moving between a task's tmux windows | [Task window navigation](docs/TMUX_NAVIGATION.md) |
| Status, signals, dashboard and troubleshooting | [Status guide](docs/STATUS.md) |
| What `status --json`, task records and adapters can rely on | [Formats](docs/FORMATS.md) |
| Cleanup protections | [Safety](docs/SAFETY.md) |
| Full command surface | [Command reference](docs/COMMANDS.md) |
| Tests, demo playground and WSL development | [Development](docs/DEVELOPMENT.md) |

## Project status and contributing

**Early-stage, Unix-only.** The command surface can change;
[CHANGELOG.md](CHANGELOG.md) lists what each release changed. See
[current limitations and possible follow-ups](docs/PROJECT_STATUS.md) before
relying on a particular workflow. The [implementation plan](docs/IMPLEMENTATION_PLAN.md)
records the original design; the guides above describe shipped behaviour.

For a change, start with the [development checks](docs/DEVELOPMENT.md) and keep
it focused. [Issues](https://github.com/wuyuz99/taskspace/issues) are the place
to report a problem or discuss a proposal.

## License

MIT - see [LICENSE](LICENSE).
