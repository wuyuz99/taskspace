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
- **`.context/` holds two notes and the signals.** `TASK.md`, `HANDOFF.md`
  and `signals/` exist; the `API_CONTRACT.md` and `STATUS.json` of the
  original sketch do not, and nothing writes notes on your behalf.
- **Little configuration.** `TSPACE_ROOT` chooses the state directory, and
  `$TSPACE_ROOT/config.json` holds the default task-root agent, the registered
  repositories, the branch prefix and the multiplexer. Nothing else is
  configurable.
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
- More built-in runtimes — plain subprocess, remote (anything else can already
  be an adapter)
