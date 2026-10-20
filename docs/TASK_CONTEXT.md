# Briefs, handoffs and task context

[Back to the README](../README.md)

## Human-readable task context

A workspace can be named after a ticket, or use any ordinary task name.
No ticket key, URL, Jira connection, or account is required.

```bash
tspace task create PAY-142 --title "Handle expired cards during checkout"
tspace task brief PAY-142 "Reject expired cards and show an inline error."
tspace task handoff PAY-142 "Backend validation done; connect the frontend next."
tspace task list
tspace task show PAY-142
```

Keep requirements and acceptance criteria in the **brief**, and local progress,
next steps, validation commands, and open questions in the **handoff**. Handoff
updates are timestamped in UTC; summaries are written by you or your agent,
not inferred from Git or process activity. `task list` shows the title and the
first non-heading line of the handoff, plus its timestamp. `attach` and `shell`
print the full context before handing over the terminal.

Both are ordinary Markdown files in the task's shared context directory:

```
$TSPACE_ROOT/worktrees/PAY-142/
├── .context/
│   ├── TASK.md       the brief
│   └── HANDOFF.md    the handoff
├── backend/          worktree
└── frontend/         worktree
```

Because `.context/` is a sibling of the worktrees, anything running in one
reaches it with a relative path — which is the point. An agent working in
`backend/` can read the task's own requirements:

```bash
cat ../.context/TASK.md
```

The agent in the `_root` window starts one level up, so for it the same file is
`.context/TASK.md`. The agent of a single-repository task runs in the worktree,
where it reaches the notes as `../.context` or `$TSPACE_CONTEXT_DIR`.

An agent command can name the task's directories, which tspace fills in when it
starts the window: `{context_dir}` for `.context`, `{task_dir}` for the directory
holding the worktrees. tspace knows no agent by name, so each uses its own flag;
for Claude Code, so it can read the notes without asking:

```bash
tspace config agent claude --add-dir {context_dir}
```

`tspace shell` and `post_create` hooks also get `TSPACE_CONTEXT_DIR`. Nothing stops you
editing, grepping, or diffing these files directly; `tspace` only reads and writes
the same two paths.

For longer Markdown notes, use your editor and import/export files:

```bash
tspace task brief PAY-142 --file brief.md
tspace task handoff PAY-142 --file handoff.md
tspace task brief PAY-142 > brief-copy.md
tspace task handoff PAY-142 --clear
tspace task brief PAY-142 --edit
tspace task handoff PAY-142 --edit
tspace task update PAY-142 --title "Validate expired payment cards"
```

Notes survive `tspace clean` — worktrees are disposable, the brief describing them
is not — and are deleted with `tspace task rm`. `tspace task rm --force` leaves the
worktrees on disk, so it leaves the notes too and tells you where they are.
`--file` copies a file's contents in; it does not link to it or keep it in sync.
Updating a handoff never changes the brief, and clearing a note removes its file,
so it has no timestamp rather than a misleading one.

`--edit` opens the note file itself using `VISUAL`, then `EDITOR`, falling back
to `vi`. Editor commands can include arguments (for example, `code --wait`); GUI
editors must wait until editing finishes. It cannot be combined with text,
`--file`, or `--clear`. Whatever the editor saved is the note — there is no draft
to reconcile, and no copy to lose if the editor or the machine dies mid-edit.
Saving an empty file means the same as `--clear`. Two editors open on one note
is the editors' business to warn about, as with any other file.

Records written before notes were files carried the Markdown inline. Reading
one shows it from the record; the first command that changes the task moves it
into `.context/`, keeping the recorded handoff time. Nothing to run, and no flag.

Optional `--ticket PAY-142` and `--ticket-url https://example.com/browse/PAY-142`
flags on `task create` and `task update` record ticket metadata locally.
Multiple differently named workspaces can reference the same ticket. Empty
values on `task update` clear those fields. `--brief-file` is also available
on create/update. Jira discovery, synchronization, and automatic updates are
not implemented.

If your task names carry their ticket key, tell tspace how to find it, and a
task created without `--ticket` gets it. The pattern is a regular expression,
so it fits whatever your tracker's keys look like; a group named `ticket`, if
any, is the ticket, else the whole match:

```bash
tspace config ticket-pattern '[A-Z][A-Z0-9]+-[0-9]+'   # ENG-123-login -> ENG-123
tspace config ticket-pattern '^gh-(?P<ticket>[0-9]+)$' # gh-42 -> 42
tspace config ticket-pattern --unset                   # off, the default
```
