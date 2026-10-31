# Changelog

What changed in each release of tspace; see [Installation](docs/INSTALL.md) to
upgrade.

## Unreleased

### Added

- **tspace remembers what every extra window runs.** `restart TASK _NAME`
  relaunches an unsaved window from how `window` opened it, and `tspace window
  TASK --save NAME`, or `W` in dash, saves one that is already open. Unsaved
  windows are remembered only while their session lasts: `start` does not
  reopen them and recipes do not carry them.
- **Rename a stopped task.** `tspace task rename OLD NEW` moves its worktrees,
  notes and session metadata while preserving uncommitted files and settings.
  `--branches` also renames unpublished branches following the configured
  task-name convention. Dash offers the same operation with `N`. A warning
  names old directories where resumable agents leave their conversations.
- **`import` accepts uncommitted changes.** Work in progress moves with the
  worktree and is reported, and nothing is committed. Conflicted files, an
  unfinished merge or rebase, a lock and a detached HEAD are still refused.
- **`import` takes several worktrees.** `tspace import PATH... --task NAME`
  checks them all before moving any, moves back the ones already moved if one
  fails, and decides the layout once, as `task create` does: with several, a
  task that had no repositories gets its agent at the root, not in the first.
  `--with-root` asks for that with one.
- **Move a repository between tasks.** `tspace repo move OLD REPO --to NEW
  [--name NAME]`, or `m` in dash, moves its worktree with uncommitted and
  untracked files, and keeps its branch, base, agent, resume command, setup
  and saved windows. It refuses while the repository's windows are open, and
  moves the worktree back if anything fails. The task's agent and root window
  settle as they do for `repo add` and `repo rm`.
- **`tspace config carry-conversations claude`** has task rename and repo move
  take Claude Code's history along with the directories they move, by renaming
  the folder under `~/.claude/projects` named after each directory, so
  `restart --resume` finds the conversation at the new path. A conversation
  that is not carried, because the new path already has one or for any other
  reason, is still named with its old directory. Off unless turned on.
- **`tspace config agent-status claude`** has status and dash read Claude
  Code's own `claude agents --json`, matching each interactive session to the
  window in its directory: `working`, `blocked` (with what it waits for) or
  `idle`, shown as `blocked (claude)`. It ranks after signals but outranks a
  `working` signal it contradicts, so an agent interrupted before its Stop
  hook no longer shows as working. Off unless turned on.
- **Which agent needs you, without hooks.** `status` and dash read the tmux
  bell and how long each window has gone without printing, so an agent with
  no `tspace signal` hooks still shows up: `bell` when its window rang, and
  `quiet` after 10 minutes of silence, a hint and never a claim that it is
  blocked. Signals still come first, and a bell outranks only `working`.
  The column is now `STATE`, and dash's detail line says where each state
  came from. `status --json` adds `agent_state` (with its `source`), `bell`
  and `output_at`. Adapters may add `BELL` and `OUTPUT` fields to `state`;
  see [Multiplexers](docs/MULTIPLEXERS.md).
- **`tspace config hooks gemini`** installs Gemini CLI hooks that report
  `working`, `blocked` and `done`, as `config hooks claude` does for Claude
  Code, in `~/.gemini/settings.json`. [Status](docs/STATUS.md#recipes-for-other-agents)
  has the hooks to paste for Codex and how to wire up any other agent.
- **`.` names the task you are in.** Inside a task's directory or one of its
  worktrees, `tspace task handoff . "API done"`, `tspace restart . backend`
  and the other per-task commands use that task; the directory decides, not
  `TSPACE_TASK`. `editor`, `attach`, `task show` and `exec -- COMMAND` can
  leave TASK out there. `stop`, `clean`, `task rm` and `repo rm` still need
  the name.
- **Start and stop one window.** `tspace stop TASK REPO` (or `--root`)
  interrupts and closes just that window, clearing its signal, and `tspace
  start TASK --root` starts only the root window. In dash, `S` and `X` do the
  same for the selected row. Adapters gain an optional `close` operation; see
  [Multiplexers](docs/MULTIPLEXERS.md).
- **`clean TASK REPO...`** cleans only the named repositories, with the same
  refusals, closing their windows instead of stopping the task. It refuses
  while an extra window that was never saved with a repository is open,
  since which worktree it uses is unknown.
- **dash shows more in less room.** A task whose windows are all quiet
  collapses to one row (space or right expands it); a dead window or a
  blocked or failed signal keeps it open. Task titles follow their names in
  the space left over. `D` cleans the selected task after confirming, as
  `tspace clean TASK` without `--force`.
- **Extra windows show up, and close one at a time.** `status` and dash list
  each task's extra windows as `_NAME` rows, saved ones marked, and `status
  --json` gains a `windows` array. `tspace stop TASK _NAME`, or `X` on its
  row, closes just that window; a saved one stays saved. `tspace restart TASK
  _NAME`, or `r`, relaunches a dead saved window with its saved command.
- **dash: `R` deletes a task**, after a y/N naming what goes: `tspace clean
  TASK --rm` without `--force`, removing worktrees, record and notes and
  keeping branches. Its refusals show on the note line.
- **dash: `-` removes a repository from a task**, after a y/N, as `tspace
  repo rm TASK REPO` does: its worktree is cleaned, with clean's refusals,
  its windows closed and its branch kept. Every warning an action gives now
  follows its result on the note line, not only the last.
- **dash: `n` creates a task**, from a form below the table: name (checked
  as it is typed), repositories picked from the registered ones or typed,
  an optional project picked from the saved ones, and whether to start it.
  It runs `tspace task create`, and the cursor goes to the new task.
- **dash: `w` opens another window** at the selected row's place, from a
  form: a shell, the task's agent or a command, an optional name, and
  whether to save it. It runs `tspace window -d`, and the cursor goes to the
  new window.
- **Bring back what a computer restart interrupted.** A task that has run
  and lost its session without `stop`, clean or closing its last window
  ending it shows as `interrupted` in `status` and dash (`"interrupted":
  true` per task in `status --json`). `tspace start --interrupted` starts all of them, resuming
  their agents' conversations; dash offers to on launch, and `I` does it.
  Each comes back as it ran: only the windows that were started and not
  closed since, a task with no repositories included, and all of them wait
  out the launch check together.
- **[Task window navigation](docs/TMUX_NAVIGATION.md)**: an opt-in tmux
  binding for a window picker limited to the current task, and the default
  one for switching tasks, with a script that checks both.
- **`task create --brief TEXT`** (and `task update --brief`) sets the brief
  inline, without writing a file for `--brief-file`.
- **An existing branch gets the base it most likely came from.** Without
  `:BASE`, `repo add` picks the branch it forked from most recently instead of
  the default, and says the base was guessed, as do `task show` and its JSON
  (`base_assumed`). `repo update --rebase` refuses a guessed base until `repo
  update --base` confirms it. Such records use format version 3; see
  [Branching](docs/BRANCHING.md).
- **Move a task's agent between windows.** `tspace task update TASK
  --agent-at-root` moves the agent out of its repository's worktree into the
  task-root window, and `--agent-in REPO` into a repository's; the agent and
  its resume command stay, the window it leaves runs its own agent or a
  shell, and the window it moves to starts it fresh. A running session is
  left alone: the command says what to restart once the agent exits, so two
  never run at once, and where a resumable agent's conversation stays.

### Changed

- **Bringing a window back resumes its agent's conversation.** When a window
  has a resume command, `start`, `open`, dash `s`/`S`, `restart` and dash `r`
  launch it with that where tspace has launched that agent before,
  instead of a fresh conversation. A resume command that exits at
  once is followed by the agent's own command. `--fresh` on `start`, `open`
  and `restart` keeps the old behaviour; a window last launched by an
  earlier version resumes from its next start on. If you have set `tspace config
  resume`, your agents now come back resumed after a computer restart; see
  [Coding agents](docs/CODING_AGENTS.md#bringing-windows-back-resumes-them).

- **`repo rm` removes what it forgets.** It cleans the repository first,
  with clean's refusals, closing its window and saved windows, then drops it
  from the record; before, the worktree and window were left where `clean`
  could no longer find them. `--keep-worktree` keeps the old behaviour.
- **Errors name what exists.** A missing `--project` lists the saved projects,
  and `repo rm` and `repo update` list the task's repositories.
- **`task create` reports its repositories only once the task exists**, so a
  later repository that fails no longer follows an "added" line for a task
  that was never made.
- **`attach` and `open` without a terminal** say so and point to `tspace
  status`, after the multiplexer's own error.

### Fixed

- **`import` takes a worktree no task claims**, wherever it is, instead of
  refusing everything under tspace's worktrees directory. A worktree left by
  `repo rm --keep-worktree` can join another task, or its old task under the
  same repository name, where it is adopted in place. One a task still lists
  is refused, naming that task and repository.
- **`repo update --shell` refuses on the repository that runs the task's
  agent**, instead of deleting the task's agent and its resume command with
  only a note. `tspace task update TASK --shell` still drops the agent on
  purpose.
- **`clean --force` removes a locked worktree** instead of failing partway,
  after the session was stopped and earlier worktrees were gone.
- **clean refuses when it cannot tell whether the session runs**, instead of
  removing the worktrees from under agents that may still be there.
- **clean keeps what an agent did while its session stopped.** It checks the
  worktrees and branches again after the stop: a change left behind keeps
  the worktree, and a commit keeps the branch under `--delete-branch`. A
  branch is deleted only if it still points to the commit found safe.
- **clean refuses when the multiplexer fails its check**, such as an adapter
  whose `check` exits non-zero, since its sessions may still run. Only a
  multiplexer, or adapter, that is not installed counts as running nothing.
- **`repo rm --keep-worktree`, `repo add` and `import` no longer guess a
  session is stopped.** When the session cannot be checked, `repo rm` of the
  agent's repository refuses, and a first `repo add` or `import` leaves the
  agent in the root window, warning why, instead of moving it while it may
  still run there. `stop` with no multiplexer installed says nothing runs.
- **A repository with no default branch** gets the branch it is on as the
  base, or its commit when detached, not `HEAD`, which `status`, `clean
  --delete-branch` and `repo update --rebase` read in the wrong place.
- **`config hooks claude` run again changes nothing**, even with hooks of
  yours after tspace's; it used to move tspace's to the end and rewrite the
  file.

- **dash: the mouse wheel moves one row per step.** Terminals turned each
  wheel step into several arrow keys, skipping rows; dash now reads the wheel
  itself and ignores it while a confirmation is open.
- **dash `t` keeps the root with single-instance terminals.** The new tab's
  command now carries `env TSPACE_ROOT=ROOT`, so a terminal that hands the
  tab to its running instance, such as Ghostty, no longer drops a non-default
  root. Windows Terminal needs `wsl.exe --exec` before the command.
- **zellij: a live session no longer shows as unknown** when `list-panes`
  answers with nothing. tspace retries a few times, then says zellij returned
  no output instead of reporting a JSON parse error.

## v0.1.1 — 2026-10-19

### Added

- **Choose your multiplexer.** `tspace config multiplexer` picks tmux (still
  the default), zellij 0.44+, or any other through a small adapter program;
  `TSPACE_MULTIPLEXER` overrides it for one command. See
  [Multiplexers](docs/MULTIPLEXERS.md).
- **Single-repository tasks run their agent in the repository,** in the
  worktree where the code is, with no `_root` window. Adding a second
  repository keeps the agent where it is and adds a root window with a shell.
  `task create --with-root` keeps the old layout.
- **`task create --start`** creates and starts a task in one step, and
  **`clean --rm`** removes the worktrees and then the task record.
- **`tspace config hooks claude`** installs Claude Code hooks that report
  `working`, `blocked` and `done` through `tspace signal`. `signal --quiet`
  makes it safe to run from hooks outside a task.
- **`restart --resume`** brings an agent back with its conversation, using a
  resume command you set with `--resume` or `tspace config resume`.
- **Ticket keys from task names** with `tspace config ticket-pattern`, and
  `tspace new` / `task new` as aliases of `task create`.
- **`repo update --branch` and `--base`** rename a task's branch or change its
  base, with `--rebase` to move the work onto the new base.
- **`config repo NAME URL`** clones a repository and registers it.
- **Saved windows:** `window --name N --save` reopens a window on every start,
  and is carried by project recipes; `--rename` and `--forget` manage them.
- **dash `t`** opens the selected task in a new terminal tab, using
  `tspace config terminal`.
- **`tspace import`** adopts a worktree you made with `git worktree add`.
- **`tspace editor`** opens a task's worktrees together in VS Code or another
  editor that reads `.code-workspace` files, configured with
  `tspace config editor`.
- **`{context_dir}` and `{task_dir}`** in an agent command are filled in with
  the task's directories when a window starts.

### Changed

- tspace no longer recognises any agent by name. The `--add-dir` it used to
  add for Claude Code is replaced by the placeholders above:
  `tspace config agent claude --add-dir {context_dir}`.
- Commands that change a task lock it, so two never interleave; reads never
  wait.
- Task records carry a format version and are upgraded the first time a
  command changes them. An older tspace refuses records written by a newer one
  instead of damaging them.
- `start` checks a second later that the session and its windows survived
  launch, and reports any that exited.
- A mistyped subcommand fails with suggestions instead of printing help.
- The README is now a short introduction, with the reference moved into
  focused guides under `docs/` (#42, thanks @dailyun). Release archives
  include the docs and this changelog.

### Fixed

- `tspace signal` reports for the window the agent was launched in, even after
  the agent changes directory.
- New tmux windows, including `window -d` and `restart`, no longer take focus
  from an attached client.
- `tspace shell` and `post_create` hooks no longer report signals for the
  window they were run from.
- `repo rm` and `repo add` no longer print notes that contradict what `start`
  and `clean` did.
- Windows tspace opens, `tspace shell` and hooks get `TSPACE_ROOT`, and a
  command run inside another root warns that it is not using it.

## v0.1.0 — 2026-10-13

First release: tasks spanning several repositories, a worktree per repository,
one tmux session per task with optional agents, briefs and handoffs, project
recipes, `status`, `dash`, signals, and `clean` that refuses to lose work.
