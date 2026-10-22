# Changelog

What changed in each release of tspace; see [Installation](docs/INSTALL.md) to
upgrade.

## Unreleased

### Added

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
- **`task create --brief TEXT`** (and `task update --brief`) sets the brief
  inline, without writing a file for `--brief-file`.
- **An existing branch gets the base it most likely came from.** Without
  `:BASE`, `repo add` picks the branch it forked from most recently instead of
  the default, and says the base was guessed, as do `task show` and its JSON
  (`base_assumed`). `repo update --rebase` refuses a guessed base until `repo
  update --base` confirms it. Such records use format version 3; see
  [Branching](docs/BRANCHING.md).

### Changed

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
