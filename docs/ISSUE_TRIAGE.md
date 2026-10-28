# Issue triage, 2026-10-20

Reviewed all 13 open issues. The order below favors protection of work, then
reliable everyday operation, then convenience. Changes are in the working
tree; this document does not mean the fixes have been released or the GitHub
issues closed.

| Priority | Issues | Resolution |
| --- | --- | --- |
| 1: protect work | #56 | Infer an existing branch's base, mark the guess in format 3 records and human/JSON output, refuse rebase until the old base is explicitly confirmed. New branches keep their known base. |
| 1: protect work | #55, dependency #54 | Selective clean; repo rm cleans before forgetting; explicit --keep-worktree escape hatch. Per-window graceful close for tmux/Zellij and optional adapter close. CLI and dashboard window start/stop. |
| 2: reliability | #60 | Retry successful whitespace-only list-panes responses three times at 50ms intervals, honor cancellation, retain session existence on error, report exhausted empty replies clearly. |
| 2: reliability | #53 | Include env TSPACE_ROOT=ROOT in the new-tab command as well as the launcher environment. The project supports Unix/WSL. |
| 2: navigation | #48 | Enable mouse reporting and handle each wheel press as exactly one row; ignore releases and wheel input during confirmations. |
| 3: workflow | #58, #59, #57 | Directory-based dot resolution; optional task for editor, attach, task show and exec -- COMMAND. Stop, clean and rm require names. #57 is covered by #59. |
| 3: dashboard | #52, #49 | Titles consume spare width; quiet tasks collapse with space/arrow expansion, while dead/blocked/failed tasks remain expanded. |
| 3: dashboard | #50 | D asks before plain clean, keeps records/branches, and reports safety refusals. |
| 3: investigation | #51 | Document an opt-in native scoped picker, tested with ten tasks on an isolated tmux server; keep cross-task selection separate. See TMUX_NAVIGATION.md. |

## Deliberate bounds

- Selective cleanup refuses while an **unsaved** extra window exists, because
  its repository ownership was never recorded. Stop the task first, or save
  the window with its repository. It closes primary and saved repository
  windows without stopping unrelated windows. This favors a safe refusal over
  deleting a worktree beneath a process of unknown ownership.
- Legacy records cannot reveal whether an old base was explicitly chosen.
  Existing metadata is preserved; the assumed-base marker applies when a
  branch is added with this version. Review an old recorded base before rebase.
- Zellij is not installed in the local WSL environment. Empty-response
  recovery and tab targeting have fake-runner coverage; the reported upstream
  intermittent response has not been reproduced locally.
- Wheel behavior has event-level regression coverage. The reporter's terminal
  and physical wheel/trackpad configuration are unknown, so that particular
  hardware behavior still needs user verification.
- The terminal fix is verified through argument/environment tests, not an
  installed Ghostty instance. A global --root flag is unnecessary for this
  Unix-only fix and is not introduced.
