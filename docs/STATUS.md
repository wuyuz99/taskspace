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

- **RUNTIME** — `running`, `dead`, `no-window`, `no-session`, `interrupted`,
  `unknown`. `interrupted` is a task with no session that was not stopped on
  purpose: one that has run, and that neither `tspace stop`, dash `x`, clean
  nor closing its last window ended, such as after a computer restart.
  `tspace start --interrupted` starts what each of them was running, and
  carries on past one that fails; dash offers to on launch, and `I` does it
  any time. `--json` carries it as each task's `interrupted`, leaving each
  row's `runtime` `no-session` as before. A session tspace did not end
  includes one whose agents all exited on a multiplexer that closes a
  window when its command ends (tmux keeps it, dead), so such a task is
  offered too; `tspace stop TASK` tells tspace it is done.
- **GIT** — `clean`, `dirty (N files)`, `missing`, `error`, plus stash and
  ahead/behind counts against the upstream, or for a branch not yet pushed,
  the commits that are not on its base (`2 commits not pushed`)

They are separate columns because a worktree can be dirty *and* running at the
same time — that is the normal state of an agent doing its job. Whether the
agent is waiting for you is a third thing, in its own column: see
[What the agent is doing](#what-the-agent-is-doing).

`tspace status --watch` redraws the table every two seconds (`--interval`), and
`--json` prints the same observations for a script.

### What the agent is doing

Once any window has something to say about its agent, `tspace status` grows
a `STATE` column, and dash always has one. It shows one answer per window,
from the most trusted source that has one:

1. **Signals**: `working`, `blocked`, `done` or `failed`, the agent's own
   word through `tspace signal`, usually from its hooks (below).
2. **Published status**, if you turn it on for an agent that publishes its
   own (below): `working`, `blocked` or `idle`, followed by the agent's name,
   such as `blocked (claude)`.
3. **The bell**: `bell`, when the window rang the terminal bell since you last
   looked at it. Something there wants you, though tspace can't say what.
4. **Silence**: `quiet`, when an agent's window has printed nothing for 10
   minutes. It is a hint that the agent may be waiting, never a claim that it
   is blocked: an agent thinking hard is quiet too. A window running a shell
   is never called quiet.

A `working` signal is the exception to that order. The agent said it needed
nothing, so a published status that says otherwise, or the bell, outranks
it. `blocked`, `done` and `failed` already ask for you and stand.

Each is followed by how long ago it was signalled, or the window last printed
(`blocked 3m`, `bell 2m`, `quiet 25m`). The bell and silence come from the
multiplexer and only for live windows. tmux tracks both for every window,
whatever your `monitor-*` options; Zellij doesn't, so there you see signals
only. An adapter can supply them; see [Multiplexers](MULTIPLEXERS.md).
`--json` carries the chosen one as each row's `agent_state`, with `state`,
`source` (`signal`, `bell`, `silence` or the agent's name), `at` and
`message`, beside the raw `signal`, `bell` and `output_at`.

#### Published status

Some agents report what each of their sessions is doing through an interface
of their own. tspace can read one, matching each session to the window that
runs in its directory, once you turn it on:

```bash
tspace config agent-status claude     # --unset turns it off
```

`claude` reads `claude agents --json`, which lists Claude Code's interactive
sessions as `busy`, `waiting` (with what it waits for) or `idle`. It needs no
hooks, and it also catches what hooks miss, such as an agent you interrupted
before its `Stop` hook could run. It costs one call to `claude` per refresh,
which is why it's off by default. An agent's published status describes the
moment, so it carries no age.

tspace never reads an agent's screen for its wording: silent reasoning looks
idle, a printed `(y/n)` looks like a prompt, and every new release of an agent
would break it.

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

Wiring an agent up takes three of its hook events: one when a prompt is
submitted (`working`), one when it needs a person (`blocked`), and one when it
finishes a turn (`done`). tspace installs them for Claude Code and Gemini CLI.
For other agents, [Recipes for other agents](#recipes-for-other-agents) has the
snippets to paste. For Claude Code, install the hooks once:

```bash
tspace config hooks claude            # --remove takes them out again
```

That adds five hooks to your user settings, `~/.claude/settings.json`, and
leaves everything else in the file alone:

```json
{
  "hooks": {
    "UserPromptSubmit": [{ "hooks": [{ "type": "command", "command": "tspace signal --quiet working" }] }],
    "Notification":     [{ "matcher": "permission_prompt|elicitation_dialog", "hooks": [{ "type": "command", "command": "tspace signal --quiet blocked" }] }],
    "PostToolUse":      [{ "hooks": [{ "type": "command", "command": "tspace signal --quiet working" }] }],
    "PostToolUseFailure": [{ "hooks": [{ "type": "command", "command": "tspace signal --quiet working" }] }],
    "Stop":             [{ "hooks": [{ "type": "command", "command": "tspace signal --quiet done" }] }]
  }
}
```

They are user-wide because a settings file in a worktree would be an untracked
file, which `tspace clean` refuses to remove. They therefore run in every Claude
Code session: `--quiet` makes `tspace signal` do nothing outside a task, and
print nothing inside one, since what a `UserPromptSubmit` hook prints is shown
to the agent.

Tool completion restores `working` after a permission prompt, including a
tool that fails and returns control to Claude. Until that tool returns, a
long-running approved tool can still show `blocked`. Each tool runs one short
signal command. To upgrade an existing installation, run the installer again
and start a new agent session; your other hooks are kept.

For Gemini CLI, `tspace config hooks gemini` does the same in
`~/.gemini/settings.json` (under `$GEMINI_CLI_HOME` if that is set), with
Gemini's events: `BeforeAgent` for `working`, `Notification` (tool permission
prompts) for `blocked`, `AfterTool` to restore `working`, and `AfterAgent`
for `done`. Gemini's hooks are on
unless `hooksConfig.enabled` is `false`. Gemini allows comments in that file
and tspace can't rewrite one that has them, so it refuses; add the hooks by
hand instead, in the same shape as the Claude Code example above.

Each installer only writes what you could write yourself. A hook you've
changed, such as `tspace signal --quiet done && say done`, is yours: running
the installer again, or `--remove`, leaves it alone.

A signal shows in the `STATE` column (`blocked 3m`), and `--json` carries the
state, message and time. A signal is the agent's word,
shown beside `RUNTIME` and never instead of it. It is one file,
`.context/signals/REPO.json` (`_root.json` for the root window), so an agent
without `tspace` on its `PATH` can write `{"state": "blocked", "message": "..."}`
there directly. `tspace stop`, `tspace clean` and a fresh `tspace start` clear a task's
signals; `tspace restart` clears the window's own.

#### Recipes for other agents

Any agent that can run a command on its own events can report. Pick the
events closest to "a prompt was submitted", "waiting on a person" and "the
turn ended", and run `tspace signal --quiet` with `working`, `blocked` and
`done`. Keep `--quiet`: some agents show a hook's output to the model, and
some refuse output that isn't JSON.

**Codex** reads user hooks from `~/.codex/hooks.json` (under `$CODEX_HOME` if
that is set), in the same shape as Claude Code. It has no notification event,
so `PermissionRequest`, which runs when a command needs approval, stands in
for `blocked`:

```json
{
  "hooks": {
    "UserPromptSubmit":  [{ "hooks": [{ "type": "command", "command": "tspace signal --quiet working" }] }],
    "PermissionRequest": [{ "hooks": [{ "type": "command", "command": "tspace signal --quiet blocked" }] }],
    "Stop":              [{ "hooks": [{ "type": "command", "command": "tspace signal --quiet done" }] }]
  }
}
```

Codex runs a new or changed hook only after you trust it: open `/hooks` in
Codex once after adding these. A hook that prints nothing leaves the approval
to you, as it would be without the hook. Codex's `notify` setting also fires
at the end of a turn, but it holds a single command, so it's often already
taken; hooks don't replace anything.

**Agents without hooks** can still be seen: dash shows a window that rang the
bell, or one quiet for 10 minutes, without any setup (see
[What the agent is doing](#what-the-agent-is-doing)). An agent you script
yourself can write `.context/signals/WINDOW.json` directly, as described above.

### Dashboard

`tspace dash` is the same information, live and full-screen, for supervising
several agents at once:

```
tspace dash  2 tasks · 1 blocked · 1 dead  22:53:17

  TASK               REPO      AGENT   RUNTIME     STATE        GIT
  checkout-redesign  (root)    claude  running     working 40s  -
                     backend   claude  running     done 12m     dirty (3 files)
>                    frontend  codex   running     blocked 3m   clean, ahead 2
  payments-fix       backend   codex   dead        -            missing

checkout-redesign/frontend  agent/checkout-redesign
  /home/me/.tspace/worktrees/checkout-redesign/frontend
  signal   blocked, 3m ago: totals endpoint: round per line or per order?
  handoff  API done, tests pass.

enter attach · space expand · s/x task · S/X window · r restart · D clean · R delete · - remove repo · t tab · c clear · n new · w window · I interrupted · q quit
```

`enter` attaches to the selected window and detaching brings you back; inside
the multiplexer it switches the client instead and the dashboard keeps running in its own
window. Every key runs the command you would have typed.

A task where nothing needs you takes one row, `(N windows)`, with the
worst runtime, the most urgent state and every git state among them; space
or right expands it and left collapses it again. A task with a dead window,
a blocked or failed signal, or a bell always shows every window. `enter`, `S` and
`X` on a collapsed task act on its root window. Each task's title follows its
name, using only the width the other columns leave.

`D` cleans the selected task, after confirming: `tspace clean TASK` without
`--force`, so it keeps the record and branches, and when work could be lost
it shows clean's refusal and changes nothing.

`R` deletes the selected task, after confirming: `tspace clean TASK --rm`,
also without `--force`, so it removes the worktrees, the record and the
notes (brief and handoff), and keeps the branches. It is a key of its own
because `D` can be undone with `s` and `R` cannot. When work could be lost,
clean's refusal shows on the note line and nothing is removed; once the task
is gone the cursor moves to the next one. A dash running inside the task's
own session refuses `D` and `R`, since ending the session would end it
halfway through; run the command from outside.

`-` removes the selected repository from its task, after confirming: `tspace
repo rm TASK REPO`, which cleans its worktree, closes its window and saved
windows, and keeps its branch. When work could be lost, clean's refusal
shows on the note line and nothing changes; `repo rm`'s notes, such as a
saved window it forgot, follow the result. It works only on a repository
row: on a collapsed task, expand it first.

`m` moves the selected repository to another task, through a form below the
table: `tspace repo move TASK REPO --to NEW`. Pick the task, and optionally
give the repository another name there. Like `-`, it works only on a
repository row. Its refusals, such as a window still open, show on the note
line and nothing changes; once it is done the cursor goes to the new task.

`n` creates a task, through a form below the table: its name, checked as
you type it (a bad or taken name is refused before anything is written),
its repositories, picked from the registered ones, with more typed as
`REPO[@BRANCH][:BASE]` words for a branch, a base or an unregistered path
(a picked name typed there too is named once, as typed), an optional
project, picked from the saved ones, and whether to start it, ticked by
default. `tab` moves between fields, `←`/`→` move along a list (one wider
than the screen scrolls, `‹` and `›` marking hidden choices), space picks
or ticks, `enter` runs `tspace task create NAME REPO... [--project P]
[--start]` and `esc` cancels without writing. The cursor then goes to the
new task. Agents, briefs and tickets are set afterwards with `tspace task
update` and `tspace task brief`.

`w` opens another window in a running task, at the selected row's place: a
repository row's worktree, a saved window's place, or otherwise the task
root. Its form takes a command (empty for a shell, `agent` for the task's
agent, otherwise a command line, split into words as a shell would without
expanding anything), an optional name, shown with its leading `_`, and
whether to save it, which needs a name. It runs `tspace window TASK [REPO |
--root] -d ...`, so dash stays in front, and the cursor goes to the new
window: `enter` then goes to it. A task that is not running says to press
`s`. These are multiplexer windows (tmux windows, Zellij tabs); tspace does
not split panes.

`S` and `X` start and stop only the selected window, as `tspace start TASK
REPO` and `tspace stop TASK REPO` (or `--root`) do; `s` and `x` act on the
whole task. Extra windows in a stopped window's worktree stay open.

Extra windows have rows of their own, under their task. `enter` goes to one,
`X` closes it (`tspace stop TASK _NAME`), `r` relaunches a dead one with the
command it was opened with, and `W` saves an open one as it stands (`tspace
window TASK --save _NAME`). A closed saved window stays saved and comes back
on the next start; `tspace window TASK --forget NAME` drops it.

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
