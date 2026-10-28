# Status and troubleshooting

[Back to the README](../README.md)

## Status

`tspace status` reports two **independent** things per repository:

```
TASK               REPO      BRANCH                   AGENT   RUNTIME     GIT
checkout-redesign  (root)    -                        claude  running     -
checkout-redesign  backend   agent/checkout-redesign  shell   running     dirty (3 files)
checkout-redesign  frontend  agent/checkout-redesign  codex   dead        clean, ahead 2
checkout-redesign  _mon      -                        htop    running     -
payments-fix       backend   agent/payments-fix       codex   no-session  missing
```

A task's first row is its `_root` window; `payments-fix` has one repository,
so its agent runs there and it has none. It has a runtime state but no git
state of its own: whatever its agent changes shows up in the repositories' rows.

Extra windows, opened with `tspace window`, follow as `_NAME` rows, marked
`(saved)` when start opens them again; a saved window that is not open shows
as `no-window`. `--json` lists them in each task's `windows` array, with
`name`, `saved`, `runtime` and, for a saved one, its `repo` and `command`.

- **RUNTIME** — `running`, `dead`, `no-window`, `no-session`, `unknown`
- **GIT** — `clean`, `dirty (N files)`, `missing`, `error`, plus stash and
  ahead/behind counts against the upstream, or for a branch not yet pushed,
  the commits that are not on its base (`2 commits not pushed`)

They are separate columns because a worktree can be dirty *and* running at the
same time — that is the normal state of an agent doing its job. `tspace` reports
only what it can observe; it will not guess that an agent is "waiting for
input", because no multiplexer can tell it that.

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
`tspace signal` works out which window it is from `TSPACE_TASK` and
`TSPACE_WINDOW`, which tspace sets in every window it launches (or, without
them, from the current directory), so it needs no arguments in an agent's
hooks, and a root agent that `cd`s into a worktree still reports for the root.
For Claude Code, install the hooks once:

```bash
tspace config hooks claude            # --remove takes them out again
```

That adds three hooks to your user settings, `~/.claude/settings.json`, and
leaves everything else in the file alone:

```json
{
  "hooks": {
    "UserPromptSubmit": [{ "hooks": [{ "type": "command", "command": "tspace signal --quiet working" }] }],
    "Notification":     [{ "hooks": [{ "type": "command", "command": "tspace signal --quiet blocked" }] }],
    "Stop":             [{ "hooks": [{ "type": "command", "command": "tspace signal --quiet done" }] }]
  }
}
```

They are user-wide because a settings file in a worktree would be an untracked
file, which `tspace clean` refuses to remove. They therefore run in every Claude
Code session: `--quiet` makes `tspace signal` do nothing outside a task, and
print nothing inside one, since what a `UserPromptSubmit` hook prints is shown
to the agent.

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
  payments-fix       backend   codex   dead        -            missing

checkout-redesign/frontend  agent/checkout-redesign
  /home/me/.tspace/worktrees/checkout-redesign/frontend
  signal   blocked, 3m ago: totals endpoint: round per line or per order?
  handoff  API done, tests pass.

enter attach · space expand · s/x task · S/X window · r restart · D clean · t tab · c clear · q quit
```

`enter` attaches to the selected window and detaching brings you back; inside
the multiplexer it switches the client instead and the dashboard keeps running in its own
window. Every key runs the command you would have typed.

A task whose windows are all quiet takes one row, `(N windows)`, with the
worst runtime, the most urgent signal and every git state among them; space
or right expands it and left collapses it again. A task with a dead window,
or a blocked or failed signal, always shows every window. `enter`, `S` and
`X` on a collapsed task act on its root window. Each task's title follows its
name, using only the width the other columns leave.

`D` cleans the selected task, after confirming: `tspace clean TASK` without
`--force`, so it keeps the record and branches, and when work could be lost
it shows clean's refusal and changes nothing.

`S` and `X` start and stop only the selected window, as `tspace start TASK
REPO` and `tspace stop TASK REPO` (or `--root`) do; `s` and `x` act on the
whole task. Extra windows in a stopped window's worktree stay open.

Extra windows have rows of their own, under their task. `enter` goes to one,
`X` closes it (`tspace stop TASK _NAME`) and `r` relaunches a dead saved one
with its saved command. A closed saved window stays saved and comes back on
the next start; `tspace window TASK --forget NAME` drops it.

For moving between a task's windows once inside it, see
[Task window navigation](TMUX_NAVIGATION.md).

The mouse wheel moves the selection one row per step. dash takes mouse input
to do that, so selecting text on its screen needs your terminal's modifier
(usually Shift).

`t` opens the selected window in a new tab of your terminal, running
`tspace attach TASK` there, so the dashboard stays where it is; like `enter`,
it needs the task to be running. Terminals each
open tabs their own way, so tell tspace yours once; it adds its command at the
end, or puts it where an argument says `{command}`:

```bash
tspace config terminal wt.exe -w 0 new-tab -- wsl.exe --exec  # Windows Terminal into WSL
tspace config terminal wezterm cli spawn --
tspace config terminal kitty @ launch --type=tab
tspace config terminal gnome-terminal --tab --
```

The command includes `env TSPACE_ROOT=ROOT`, so the selected root survives a
terminal delegating the new tab to its existing process. The launcher also
receives the environment. The command must execute in the same Unix/WSL
environment as tspace; a Windows Terminal configuration needs the WSL bridge.

## Troubleshooting

- A missing or dead session/window: see [opening and restarting windows](WORKSPACE.md#enter-the-whole-workspace) and [recovering an agent](CODING_AGENTS.md#recover-a-stopped-agent).
- A refused cleanup: see [safety checks](SAFETY.md#safety); inspect the reported changes before using force.
- A fresh worktree missing local dependencies or configuration: see [setup hooks](WORKFLOWS.md#fresh-worktrees-are-empty-post_create).
- An older or incompatible session: see [multiplexer session names and upgrades](MULTIPLEXERS.md#session-names-and-upgrades).
