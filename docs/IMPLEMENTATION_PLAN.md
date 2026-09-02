# Implementation plan — `aw` v0.1

> Worktrees are per-repository. Features aren't.

This document records what we are building and, more importantly, *why* each
decision was made. `task.md` holds the requirements; this holds the engineering
rationale. If you are a new contributor, read this first.

Status: **v0.1, pre-release.** Nothing here is stable.

---

## 1. Language: Go

Go. The decision took one paragraph and should not be relitigated.

This tool's hot path is `exec("git")` and `exec("tmux")`. It parses their
output, writes small JSON files, and prints a table. Every criterion that
matters — a single static binary users can drop on a PATH, first-class
`os/exec` and signal handling, a fast edit/test loop, a low barrier for drive-by
contributors, and Bubble Tea waiting for the eventual TUI — favours Go. Rust's
real advantages (zero-cost abstraction, borrow-checked concurrency, raw
throughput) buy nothing for a process orchestrator that spends its life blocked
on subprocesses, and cost compile time and contributor reach.

Dependencies stay minimal: `spf13/cobra` for the CLI, and the standard library
for everything else. No YAML library, no ORM, no DI framework.

---

## 2. Data model

Three concepts, and only three:

```
Task    a logical unit of work, e.g. "checkout-redesign". The first-class object.
Repo    a source repository enrolled in a task, plus its branch/worktree/agent.
Runtime the tmux session backing a task while it is running.
```

The Go types, approximately:

```go
type Task struct {
    Name  string          `json:"name"`
    Repos map[string]Repo `json:"repos"` // keyed by repo name
}

type Repo struct {
    Path       string   `json:"path"`        // canonical abs path to the source repo
    Branch     string   `json:"branch"`      // agent/<task>
    Base       string   `json:"base"`        // resolved base ref, recorded at creation
    Worktree   string   `json:"worktree"`    // <root>/<task>/<repo>
    PostCreate string   `json:"post_create,omitempty"`
    Agent      []string `json:"agent,omitempty"` // argv; empty means "user's shell"
}
```

An agent is `[]string`. There is deliberately no `AgentAdapter` interface — see
`task.md`. `exec.LookPath(argv[0])` is the whole of "detection", and a
four-method interface over two trivial implementations is the speculative
architecture we are trying to avoid.

**No runtime state appears in these structs.** That is the point of the next
section.

---

## 3. On-disk state

```
$AW_ROOT/                      default ~/.aw
├── tasks/
│   ├── checkout-redesign.json
│   └── payments-fix.json
└── worktrees/
    └── checkout-redesign/
        ├── .context/          reserved, unused in v0.1
        ├── backend/           git worktree
        └── frontend/          git worktree
```

Three decisions worth defending:

**One JSON file per task, not one global file.** The premise of the tool is many
concurrent tasks. A single state file means two `aw` processes racing on a
read-modify-write and losing an update. Per-task files mean invocations against
different tasks never touch the same bytes, so we need no lockfile at all.
Writes go through a temp file plus `os.Rename` for atomicity within a task.

**JSON, not YAML.** The file is machine-written and only incidentally read by
humans. JSON is in the standard library; YAML is a dependency plus a surprising
spec. `aw task show` is where humans should read task state, and it can format
however we like.

**Runtime state is derived, never persisted.** No PIDs, no "running" booleans,
no last-known-status cache. Every command asks tmux and git for the truth at the
moment it runs. This is the single most important simplification in the design:
there is no stale state, no reconciliation pass, no repair command, and no class
of bug where the file disagrees with reality. It follows directly from the
project principle "real Git state over duplicated state".

`$AW_ROOT` is overridable because the user already keeps workspace directories
in a chosen, visible location, and a hidden `~/.aw` should not be forced onto an
existing habit. This is the *only* configuration knob in v0.1.

---

## 4. CLI structure

```
aw init                              create/report state dirs (never required)

aw task create NAME
aw task list
aw task show NAME
aw task rm NAME                      refuses while worktrees exist

aw repo add TASK PATH [--name N] [--agent CMD...] [--base REF] [--post-create SH]
aw repo rm TASK REPO

aw start TASK [REPO...]              create worktrees + launch runtime
aw stop TASK                         SIGINT, grace, kill; worktrees untouched
aw attach TASK                       title the tab, then attach/switch-client
aw shell TASK [REPO]                 ad-hoc interactive shell in a worktree
aw path TASK [REPO]                  print path only, for cd "$(aw path ...)"
aw exec TASK [-- CMD...]             run CMD in every worktree
aw status [TASK]                     two-axis table
aw clean TASK [--delete-branch] [--force]
```

Package layout:

```
cmd/aw/            main
internal/cli/      cobra commands; the ONLY package that formats for humans
internal/task/     Task/Repo model, load/save, name validation
internal/git/      repo validation, base-ref resolution, worktree lifecycle, dirty/safety checks
internal/runtime/  Runtime interface + tmux implementation
internal/status/   aggregation across git + runtime; the notification choke point
internal/exec/     testable command runner
```

No `internal/agent/`. Boundaries that matter: `internal/git` never formats
output, tmux vocabulary never escapes `internal/runtime`, and `internal/task`
knows nothing about either.

`internal/exec` is a small interface (`Run(ctx, dir, argv) (stdout, stderr, err)`)
with a real implementation and a recording fake. It exists so we can unit-test
*argument construction* — the thing most likely to be silently wrong — without
spawning processes. It is not dependency injection for its own sake; commands
take a runner and default to the real one.

---

## 5. Git worktree lifecycle

`aw start` per repo:

1. **Validate** the source repo: path exists, `git rev-parse --git-dir` succeeds,
   not mid-rebase/merge/bisect.
2. **Resolve the base ref**, in order: `origin/HEAD` → local default branch →
   current `HEAD`. Print what was chosen. This exists because branching from
   whatever `HEAD` happens to be silently inherits an unrelated feature branch —
   a data-correctness bug, not a UX wrinkle. `--base` overrides, and the
   resolved value is recorded in the task file.
3. **Reconcile existing state**, in this order:
   - worktree path registered and present → reuse it, no-op;
   - worktree registered but directory missing → `git worktree prune`, recreate;
   - branch exists and is checked out elsewhere → hard error naming the other
     worktree (this is the most common real conflict);
   - branch exists, not checked out → attach a worktree to it, do not re-fork;
   - nothing exists → `git worktree add -b <branch> <path> <base>`.
4. **Run `post_create`** if set, in the new worktree, via `sh -c`. A fresh
   worktree has no `.env`, `node_modules`, or `venv`; without this hook the tool
   is not usable on a real project on day one. Failure is reported loudly but
   does not roll back the worktree — a half-provisioned worktree the user can
   inspect beats silently destroying one.

Git errors are surfaced with the failing argv and git's own stderr. We never
paraphrase git.

---

## 6. tmux runtime model

```go
type Runtime interface {
    Available() error
    Start(task string, windows []Window) error
    Stop(task string) error
    Attach(task string) error
    OpenShell(task, window, dir string) error
    Sessions() (map[string]SessionState, error)
}
```

One tmux session per task (`aw-<task>`), one window per repo named after the
repo. That mapping is not arbitrary: the user's top pain point is not knowing
which terminal tab belongs to which workspace, and a named-window-per-repo
session makes tmux's own status line answer the question for free.

Details that are bugs if skipped:

- **Session names are sanitized.** `.` and `:` are tmux target syntax
  (`session:window.pane`), so a task named `checkout.redesign` breaks every
  `-t` we issue. Sanitize once, store the result.
- **`Attach` uses `switch-client` when `$TMUX` is set**, else `attach-session`.
  Otherwise nesting produces "sessions should be nested with care".
- **Panes survive command exit** (`remain-on-exit`). If a crashed agent's window
  vanishes, so does the traceback explaining the crash — which is precisely when
  the user needs it.
- **All parsing uses `-F` format strings**, never human-readable output.
- **Stop is SIGINT → grace → kill**, not an immediate `kill-session`, because a
  hard kill can land mid-edit.

Terminal tab titling lives next to this: before handing off, `aw attach` and
`aw shell` emit `ESC ] 2 ; aw: <task> BEL` so the *hosting* tab is labelled too,
guarded by a TTY check plus `--no-title` / `AW_NO_TITLE`. Emitting escape
sequences into a pipe would corrupt it.

Zellij and a plain detached-subprocess runtime are the intended second
implementations; the interface above is sized for them, and for nothing more.

---

## 7. Safety strategy

The governing rule: **prefer refusing an operation over losing code.**

A worktree is **dirty** if it has tracked modifications, staged changes,
untracked files, or stashes created in it. Locked worktrees are also treated as
unsafe — `git worktree remove` simply fails on them, and we would rather say so
than surface a raw git error. Submodules are detected and reported but not
recursed into; this is a documented limitation, not an oversight.

A branch's work is **safe to discard** only if it is merged into the resolved
base branch, *or* pushed to a tracked upstream. Without both halves of that
test, a never-pushed branch could never be cleaned at all — every commit on it
is "unique" by construction. Anything else requires `--force`.

`aw clean` therefore: stops the runtime, inspects every worktree, refuses as a
whole if any worktree is dirty (listing exactly which files, in which repo),
removes the worktrees that are safe, and **leaves branches alone** unless
`--delete-branch` is passed — and even then applies the safety test above.

`aw clean` and `aw task rm` are deliberately different operations: `clean` tears
down worktrees and keeps the task record; `task rm` deletes the record and
refuses while worktrees still exist. Conflating them is how a tool ends up
deleting something the user only wanted to tidy.

---

## 8. Status

Two orthogonal axes, never collapsed into one enum:

```
TASK               REPO      BRANCH                   AGENT   RUNTIME     GIT
checkout-redesign  backend   agent/checkout-redesign  claude  running     dirty (3 files)
checkout-redesign  frontend  agent/checkout-redesign  codex   dead        clean, ahead 2
payments-fix       backend   agent/payments-fix       codex   no-session  missing
```

- `RUNTIME`: `running` | `dead` | `no-session` | `unknown`
- `GIT`: `clean` | `dirty (N)` | `missing` | `error`, with ahead/behind markers

A worktree can be dirty *and* running — that is the normal state of an agent
doing its job, so a single status column would be actively misleading.

Repos are queried concurrently; `git status` across several large repositories
is the dominant cost of this command. We report only what we can observe.
Semantic agent states (`waiting_for_input`, `blocked`) are not guessed.

`internal/status` is the single choke point where a future notifier attaches.
That is the only concession v0.1 makes to the eventual signaling feature.

---

## 9. Testing strategy

Integration over mocks, wherever the thing under test is git.

- **Git tests build real temporary repositories** (`t.TempDir()`, `git init`,
  commit, sometimes a bare "remote" to exercise upstream/ahead/behind). Mocking
  git's behaviour would test our idea of git rather than git.
- **Command construction is unit-tested** through the recording runner in
  `internal/exec`. Wrong argv is the most likely silent defect in a tool that is
  mostly argv.
- **tmux tests `t.Skip()` when tmux is absent** so the suite stays green on
  machines and CI runners without it.
- Claude Code and Codex are never required. Agent commands in tests are `sleep`,
  `cat`, and `false`.

Coverage targets, at minimum: task persistence including atomic write;
invalid-repo handling; base-ref resolution order; worktree creation, reuse, and
the checked-out-elsewhere conflict; dirty detection including stashes; the
safe-to-discard predicate; cleanup refusal paths; session-name sanitization;
tmux state parsing.

**Development on Windows.** v0.1 targets Unix only. Unit tests run natively;
tmux integration tests are cross-compiled (`GOOS=linux go test -c`) and executed
under WSL, which supplies real tmux and git, so no Go toolchain is needed inside
WSL. End-to-end runs use a `GOOS=linux` build of the binary in the same way.

---

## 10. Known gaps, accepted for v0.1

- Unix only. No Windows support, by design.
- Submodules detected, not recursed.
- No notifications; only the structural choke point for them.
- `.context/` location reserved but unimplemented.
- Single configuration knob (`AW_ROOT`); no config file system yet.
- The module path `github.com/aw-tool/aw` is a placeholder pending a decision on
  where this repository lives.
