# Project status

[Back to the README](../README.md)

## Current limitations

- **Unix only.** A multiplexer (tmux, zellij or an adapter's) is required for
  sessions; there is no Windows path.
- **Submodules** are detected and reported, but not recursed into.
- **No notifications.** `internal/status` is the single place one would attach.
- **Windows you open yourself get no `TSPACE_*` variables.** The windows tspace
  opens, `tspace shell` and hooks do; a pane you split or a window you open in
  the multiplexer starts from its environment.
- **Briefs and handoffs render with control characters stripped.** The stored
  Markdown keeps every byte; what reaches the terminal cannot move the cursor or
  retitle the tab, because a brief is usually pasted in from somewhere else.
- **Stash attribution is a heuristic.** git keeps one stash stack per
  repository, so stashes are matched by the branch recorded in their message.
  It errs toward reporting a stash, which is the safe direction.
- **`.context/` holds two notes, the signals and the editor workspace.**
  `TASK.md`, `HANDOFF.md`, `signals/` and `TASK.code-workspace` exist; the
  `API_CONTRACT.md` and `STATUS.json` of the original sketch do not, and
  nothing writes notes on your behalf.
- **Little configuration.** `TSPACE_ROOT` chooses the state directory, and
  `$TSPACE_ROOT/config.json` holds the default task-root agent and its resume
  command, the registered repositories, the branch prefix, the ticket pattern,
  the multiplexer, the terminal and the editor. Nothing else is configurable.
- Existing sessions from the old `aw@...` or `aw-...` naming schemes require manual inspection
  and stopping or renaming before starting their replacement.

## Roadmap

tspace is the layer underneath coding agents: the task that spans several
repositories, and the worktrees, branches and sessions that go with it.
Agents now ship their own parallel worktrees, subagents and command centers,
and many tools manage agents in parallel worktrees of one repository. What few
model is one feature across several repositories, in the terminal, for any
agent. That is where tspace stays, neutral about which agent and which
multiplexer you use.

**dash is how people use tspace; the CLI is how everything else does.** dash is
where you see which agent is blocked, which finished and where to go next, and
act on it. The CLI stays complete underneath: agents run `tspace signal` and
`tspace task handoff` from their windows, hooks and scripts run it, and
`status --json` is how anything else reads state. Every dash action is also a
command, so nothing a person can do is out of reach of a script or an agent.
dash leaves windows, panes and layouts to the multiplexer.

### Toward 1.0

1.0 means the core can be relied on without reading the code:

- **It never loses work.** Every command that removes or rewrites something
  refuses when it cannot tell whether that is safe, not only when it can tell
  that it is not ([#87](https://github.com/wuyuz99/taskspace/issues/87)).
- **It survives a restart.** After the machine restarts, a task comes back
  with exactly what was running
  ([#85](https://github.com/wuyuz99/taskspace/issues/85)).
- **Its formats are stable.** Task records, `status --json` and the adapter
  protocol only change in ways older readers can still read.

The way there is a few waypoints, each a release once its work is done, not
on a date. Fixes ship as patch releases in between. Each waypoint shows up in
dash, not only in the CLI.

- **0.2 — Nothing lost.** Every command that removes or rewrites refuses when
  it cannot tell ([#87](https://github.com/wuyuz99/taskspace/issues/87)), and
  the clean and recovery fixes made since 0.1.1 are released. In dash, a
  refused action says why, and what to do about it.
- **0.3 — Comes back.** Recovery restores exactly what was running
  ([#85](https://github.com/wuyuz99/taskspace/issues/85)), tested against one
  fake multiplexer that behaves like tmux and zellij do
  ([#88](https://github.com/wuyuz99/taskspace/issues/88)); dash's offer on
  launch brings back just that. Operations move into one place that dash and
  the CLI both call ([#86](https://github.com/wuyuz99/taskspace/issues/86)),
  so dash can grow without a second copy of the logic.
- **0.4 — Settled.** Task records, `status --json` and the adapter protocol
  are documented as stable. The first run goes through dash: `tspace` with no
  arguments opens it, and an empty dash walks through registering a
  repository and creating a first task. Running tspace on a remote machine
  over SSH, where the code and the agents are, is documented as a workflow.
- **1.0** — all of that holds up on real work: several tasks across the usual
  repositories, at least two of them running at once, through agent exits and
  restarts, interrupted setup, a computer restart, and cleanup that has to
  refuse because of valuable local changes.

### Next

**Which agent needs you, for every agent**
([#94](https://github.com/wuyuz99/taskspace/issues/94)). Every agent now runs
in parallel worktrees by itself; telling at a glance which one is blocked is
what a dashboard over all of them is for. Status comes from every source an
agent already offers, most trusted first, and dash shows where it came from:

- Hooks, for more agents: `config hooks codex` and `config hooks gemini` next
  to the Claude one, all on `tspace signal`
- Status an agent publishes itself, such as Claude Code's `claude agents
  --json`, as an opt-in integration
- What the multiplexer knows of any agent: a dead pane, the bell, time since
  the last output
- A silence guess, last, and labelled as one

dash shows what needs you first: blocked and finished agents ahead of the
rest. tspace does not read agents' screens for their wording.

**From task to merged.** A task today goes from create to clean. A feature
that spans two repositories ends as two pull requests, and nothing yet
follows it there:

- The task's pull requests and CI status as columns in dash, and in
  `status`, one row per repository
- The pull requests linked to each other, and merge readiness for the task as
  a whole: every repository pushed, reviewed and green
- A review workspace: open someone else's cross-repository change as a task,
  in one step

These come as an opt-in integration named after its forge, such as GitHub, on
top of a neutral interface, the way `config hooks claude` sits on top of
`tspace signal`.

**Reference repositories**
([#95](https://github.com/wuyuz99/taskspace/issues/95)): a repository the task
needs only to read, checked out on its base with no task branch and made
read-only, so an agent can see it and cannot change it.

### Later

- Agent instructions at the task root: an `AGENTS.md` written from the brief,
  which agents from many vendors read, so the task's context reaches them
  without `{context_dir}` in their command
- Desktop notifications, if dash showing what needs you first turns out not
  to be enough
- A neutral status protocol, such as the draft OSC 7501 states, once agents
  and multiplexers settle on one; it would replace most per-agent hooks
- One dash across several machines, if real use calls for it: it reads
  `tspace status --json` over SSH from each host and attaches with
  `ssh -t HOST tspace attach TASK`. tspace itself keeps running where the code
  is; driving a remote multiplexer from another machine is not planned
- A plain-subprocess runtime, for agents that need no multiplexer (anything
  else can already be an adapter)

### Not planned

Coordinating agents is left to the agents and the tools built for it:

- `tspace run TASK "prompt"`, or anything else that sends agents work
- Dependencies between agents, such as "frontend waits for the backend API
  contract"
- Files that agents write to coordinate through, such as a shared API
  contract or status file. The brief and handoff stay notes for people.
