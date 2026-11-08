# Formats

[Back to the README](../README.md)

What tspace promises about the formats other programs and other versions of
tspace read. The rule for all of them is the same: a change never stops an
older reader from reading. Fields and values may be added, so a reader must
ignore what it does not know. Nothing is renamed, retyped or removed.

Tables and other text output are for people and may change at any time.

## `status --json`, `task list --json`, `task show --json`

`status --json` prints an array with one object per task, and always an array,
even for one task or none. `task list --json` prints an array of the objects
`task show --json` prints.

A field that has no value may be left out, or be `null` where the
documentation below says so. `root` is `null` for a task with no root window.

In `status --json`, a runtime is one of `running`, `dead`, `no-window`,
`no-session` or `unknown`. An interrupted task's windows say `no-session`, and
the task's own `interrupted` is true. A worktree's `git` is one of `clean`,
`dirty`, `missing` or `error`.

A reference, a repository the task only reads, has `reference` true. In
`status --json` its `branch` is empty, its `base` is what it is checked out
at, its `runtime` is `no-window`, and `behind` counts the commits on its base
it does not have. `task show --json` gives its `base` and an empty `branch`.

A task's `kind` is free text the person gave with `--kind`, such as `review`,
or absent. tspace gives it no meaning; `status --json` has it too.

`signal.state` is one of `working`, `blocked`, `done` or `failed`.
`agent_state.state` is one of those, or `bell` or `quiet`. `agent_state.source`
is `signal`, `bell` or `silence`. A reader should treat a state or source it
does not know as nothing to act on. Times are RFC 3339.

## Task records

`$TSPACE_ROOT/tasks/TASK.json` is one JSON file per task, with a format
`version`. A tspace reads every version from 1 up to its own and refuses a
newer one, leaving it untouched, so downgrading loses access to a newer record
and never damages it. A record with no version is from before v0.1.1 and is
refused too; change it once with tspace v0.3 to bring it up to date.

A record is written in the oldest format that holds everything in it. A field
an older tspace would drop on saving gets a new version, which that older
tspace then refuses. Version 2 adds saved windows, version 3 guessed bases and
version 4 reference repositories and a `kind`, so a task with either needs a
tspace from after 0.4.

The record and the notes beside it in `.context` are for tspace to read and
write. Edit a record by hand at your own risk; the notes are yours.

`config.json` and project recipes change only by adding optional fields. A
recipe's `reference` is one: a tspace from before it makes that repository an
ordinary one in the task it creates.
`run/` is session memory, not a format: nothing outside tspace should read it.

## Adapter protocol

The protocol in [Multiplexers](MULTIPLEXERS.md) is version 1, passed to `check`.
Within a version, operations and arguments keep their meaning, and new fields
on `state`'s lines are optional. `rename`, `close` and `label` are optional
operations: an adapter without them fails them, and only that feature is
unavailable. A later release may add optional operations on the same terms, so an existing
adapter keeps working without them. A change an existing adapter could not
follow gets a new version, which such an adapter refuses in `check`.
