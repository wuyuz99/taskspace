// Package status aggregates git and runtime state into what tspace shows the user.
//
// Every status query in tspace funnels through an Aggregator, so that each view
// of a task sees the same thing.
package status

import (
	"context"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/wuyuz99/taskspace/internal/fspath"
	tsgit "github.com/wuyuz99/taskspace/internal/git"
	"github.com/wuyuz99/taskspace/internal/runtime"
	"github.com/wuyuz99/taskspace/internal/task"
)

// RuntimeState is what we can observe about a window's process.
type RuntimeState string

const (
	// RuntimeRunning means the window exists and its process is alive.
	RuntimeRunning RuntimeState = "running"
	// RuntimeDead means the window exists but its command has exited.
	RuntimeDead RuntimeState = "dead"
	// RuntimeNoWindow means the session exists but has no window for this repo.
	RuntimeNoWindow RuntimeState = "no-window"
	// RuntimeNoSession means nothing is running for this task.
	RuntimeNoSession RuntimeState = "no-session"
	// RuntimeInterrupted means nothing is running for this task, and it was
	// not stopped on purpose: a computer restart, say, took its session.
	RuntimeInterrupted RuntimeState = "interrupted"
	// RuntimeUnknown means the runtime could not be queried.
	RuntimeUnknown RuntimeState = "unknown"
)

// GitState is the worktree's condition.
type GitState string

const (
	GitClean   GitState = "clean"
	GitDirty   GitState = "dirty"
	GitMissing GitState = "missing"
	GitError   GitState = "error"
)

// RepoStatus is one row of `tspace status`.
//
// Runtime and Git are separate on purpose. A worktree can be dirty and running
// at the same time — that is the normal state of an agent doing its job — so
// collapsing them into a single column would be actively misleading.
type RepoStatus struct {
	Task    string
	Repo    string
	Branch  string
	Agent   string
	Runtime RuntimeState
	Git     GitState

	// Detail carries the observed git numbers. Zero when Git is Missing or Error.
	Detail tsgit.Status
	// Unpushed counts the commits on a branch with no upstream that are not on
	// its base: work that exists only on this machine. Detail's ahead/behind
	// need an upstream, which a new task branch does not have until pushed.
	Unpushed int
	// Signal is what the window's agent last reported, if anything. It is the
	// agent's word, shown beside Runtime and never folded into it.
	Signal *task.Signal
	// Pane is what the multiplexer saw of the window beyond Runtime: its bell
	// and when it last printed. It is zero without a live window.
	Pane runtime.PaneState
	// Report is the window's most trusted account of what its agent is doing:
	// Signal, the bell, or a long silence. It is nil when none says anything.
	Report *Report
	// Err is the error behind GitError, if any.
	Err error
}

// TaskStatus is a task and its repositories.
type TaskStatus struct {
	Task    string
	Session string
	Running bool
	// Interrupted says the session is gone without stop or clean ending it;
	// its windows' runtime is then RuntimeInterrupted.
	Interrupted bool
	// Root is the task-root window, which has a process but no worktree. It
	// is nil for a single-repo task, whose agent runs in the repository.
	Root  *RootStatus
	Repos []RepoStatus
	// Extra are the task's extra windows: those saved with it, open or not,
	// and those open in its session without being saved, by name.
	Extra []ExtraStatus
	// RuntimeErr is set when the runtime could not be queried at all.
	RuntimeErr error
}

// RootStatus is the `(root)` row of `tspace status`.
type RootStatus struct {
	Agent   string
	Runtime RuntimeState
	Signal  *task.Signal
	Pane    runtime.PaneState
	Report  *Report
}

// ExtraStatus is a window opened with `tspace window`, beside the root and
// repository windows. It has no signal or git state of its own.
type ExtraStatus struct {
	// Name is the window's name in the session, with its leading '_'.
	Name string
	// Saved says the task's record keeps it, so start opens it again. Repo
	// and Command are then the record's; an unsaved window's are unknown.
	Saved   bool
	Repo    string
	Command []string
	// Running is the command the multiplexer sees in an unsaved window.
	Running string
	Runtime RuntimeState
}

// SignalReader supplies what agents have reported about themselves.
type SignalReader interface {
	ReadSignals(taskName string) (map[string]task.Signal, error)
}

// RunReader says whether a task without a session was interrupted, and what
// was running when it was.
type RunReader interface {
	Interrupted(taskName string) bool
	Running(taskName string) (task.Running, bool)
}

// SessionObserver is told every session state status reads, so what a session
// runs is kept up to date between commands. This is the one thing a status
// query can write: session memory, under the task's lock, and only on a change.
type SessionObserver interface {
	Observe(ctx context.Context, t *task.Task, st runtime.SessionState)
}

// PublishedReader supplies what agents publish about themselves, keyed by
// the directory each agent runs in. It is an opt-in integration with one
// agent's own interface; core status never reads an agent by name.
type PublishedReader interface {
	Published(ctx context.Context) (map[string]Published, error)
}

// Aggregator collects status across git and the runtime.
type Aggregator struct {
	Git     *tsgit.Git
	Runtime runtime.Runtime
	// Signals is optional; without it no row carries a signal.
	Signals SignalReader
	// Run is optional; without it no task is interrupted, only no-session.
	Run RunReader
	// Observer is optional; without it status only reads.
	Observer SessionObserver
	// Now is the clock silence is measured by; nil is time.Now.
	Now func() time.Time
	// Published are the agent status integrations turned on, if any.
	Published []PublishedReader
	// TaskDir is the directory a task's root window runs in, for matching
	// published status to it; without it, only repository windows match.
	TaskDir func(taskName string) string

	gitSlots chan struct{}
}

// gitLimit bounds the worktrees inspected at once. Each is a few processes, and
// a dashboard with many tasks refreshes them all, every couple of seconds.
const gitLimit = 8

// New returns an Aggregator.
func New(g *tsgit.Git, rt runtime.Runtime) *Aggregator {
	return &Aggregator{Git: g, Runtime: rt, gitSlots: make(chan struct{}, gitLimit)}
}

// gitSlot waits for a turn to run git, and returns what gives it back. A
// cancelled ctx goes ahead, for git to fail as it would have.
func (a *Aggregator) gitSlot(ctx context.Context) func() {
	select {
	case a.gitSlots <- struct{}{}:
		return func() { <-a.gitSlots }
	case <-ctx.Done():
		return func() {}
	}
}

// Task reports the status of one task.
//
// Repositories are queried concurrently: `git status` across several large
// repositories is the dominant cost of this command, and they are independent.
func (a *Aggregator) Task(ctx context.Context, t *task.Task) TaskStatus {
	return a.task(ctx, t, a.published(ctx), nil)
}

// published asks every integration once, for all tasks together: an agent's
// own interface may take a while to answer. One that fails says nothing, as
// an unreadable signal does.
func (a *Aggregator) published(ctx context.Context) map[string]Published {
	out := map[string]Published{}
	for _, r := range a.Published {
		got, err := r.Published(ctx)
		if err != nil {
			continue
		}
		for dir, p := range got {
			if _, ok := out[fspath.Key(dir)]; !ok {
				out[fspath.Key(dir)] = p
			}
		}
	}
	return out
}

// sessions asks the multiplexer about every session at once, if it can. nil
// says to ask per task: a failure here says nothing about any one session.
func (a *Aggregator) sessions(ctx context.Context) map[string]runtime.SessionRead {
	lister, ok := a.Runtime.(runtime.StateLister)
	if !ok {
		return nil
	}
	all, err := lister.States(ctx)
	if err != nil {
		return nil
	}
	return all
}

// session is one session's state, from sessions' answer if there is one.
func (a *Aggregator) session(ctx context.Context, name string, known map[string]runtime.SessionRead) (runtime.SessionState, error) {
	if known == nil {
		return a.Runtime.State(ctx, name)
	}
	if r, ok := known[name]; ok {
		return r.State, r.Err
	}
	return runtime.SessionState{Name: name, Panes: map[string]runtime.PaneState{}}, nil
}

func (a *Aggregator) task(ctx context.Context, t *task.Task, published map[string]Published, known map[string]runtime.SessionRead) TaskStatus {
	out := TaskStatus{Task: t.Name}
	pub := func(dir string) *Published {
		if p, ok := published[fspath.Key(dir)]; ok && dir != "" {
			return &p
		}
		return nil
	}

	var sess runtime.SessionState
	// A runtime we could not query is not the same as a runtime with nothing
	// running. Reporting a broken tmux server as "no-session" would tell the
	// user their agents are stopped when we simply do not know.
	runtimeUnknown := a.Runtime == nil
	if a.Runtime != nil {
		out.Session = a.Runtime.SessionName(t.Name)
		st, err := a.session(ctx, out.Session, known)
		if err != nil {
			runtimeUnknown = true
			out.RuntimeErr = err
			sess = runtime.SessionState{Panes: map[string]runtime.PaneState{}}
		} else {
			sess = st
			out.Running = st.Exists
		}
	}

	// Unreadable signals are treated as none: they are a courtesy from the
	// agent, and status must still report what it can observe.
	var signals map[string]task.Signal
	if a.Signals != nil {
		signals, _ = a.Signals.ReadSignals(t.Name)
	}
	signal := func(window string) *task.Signal {
		if s, ok := signals[window]; ok {
			return &s
		}
		return nil
	}

	now := time.Now()
	if a.Now != nil {
		now = a.Now()
	}

	if t.HasRootWindow() {
		root := &RootStatus{
			Agent:   t.AgentLabel(),
			Runtime: a.windowState(sess, task.RootWindowName, runtimeUnknown),
			Signal:  signal(task.RootWindowName),
			Pane:    a.livePane(sess, task.RootWindowName, runtimeUnknown),
		}
		var rootPub *Published
		if a.TaskDir != nil {
			rootPub = pub(a.TaskDir(t.Name))
		}
		root.Report = report(root.Signal, root.Runtime, root.Pane, rootPub, len(t.RootAgent()) > 0, now)
		out.Root = root
	}

	names := t.RepoNames()
	rows := make([]RepoStatus, len(names))

	var wg sync.WaitGroup
	for i, name := range names {
		repo := t.Repos[name]
		wg.Add(1)
		go func() {
			defer wg.Done()
			rows[i] = a.repoStatus(ctx, t.Name, name, repo, sess, runtimeUnknown)
			agent := t.RepoAgent(name)
			rows[i].Agent = task.Repo{Agent: agent}.AgentLabel()
			rows[i].Signal = signal(name)
			rows[i].Pane = a.livePane(sess, name, runtimeUnknown)
			rows[i].Report = report(rows[i].Signal, rows[i].Runtime, rows[i].Pane, pub(repo.Worktree), len(agent) > 0, now)
		}()
	}
	wg.Wait()

	out.Repos = rows
	out.Extra = a.extraWindows(t, sess, runtimeUnknown)
	if !runtimeUnknown && a.Observer != nil {
		a.Observer.Observe(ctx, t, sess)
	}
	if !runtimeUnknown && !sess.Exists && a.Run != nil && a.Run.Interrupted(t.Name) {
		out.Interrupted = true
		// Only what comes back says so; the rest simply is not running.
		r, _ := a.Run.Running(t.Name)
		if out.Root != nil && (r.Whole || r.Root) {
			out.Root.Runtime = RuntimeInterrupted
		}
		for i := range out.Repos {
			if r.Whole || slices.Contains(r.Repos, out.Repos[i].Repo) {
				out.Repos[i].Runtime = RuntimeInterrupted
			}
		}
		for i := range out.Extra {
			if r.Whole || slices.Contains(r.Windows, strings.TrimPrefix(out.Extra[i].Name, task.ExtraWindowPrefix)) {
				out.Extra[i].Runtime = RuntimeInterrupted
			}
		}
	}
	return out
}

// extraWindows lists t's saved windows in record order, then any other
// extra window open in sess, by name. Only a window whose name tspace could
// have given it is listed, so every row can be closed by that name.
func (a *Aggregator) extraWindows(t *task.Task, sess runtime.SessionState, runtimeUnknown bool) []ExtraStatus {
	var out []ExtraStatus
	for _, w := range t.Windows {
		out = append(out, ExtraStatus{Name: w.SessionName(), Saved: true, Repo: w.Repo, Command: w.Command,
			Runtime: a.windowState(sess, w.SessionName(), runtimeUnknown)})
	}
	if a.Runtime == nil {
		return out
	}
	known := map[string]bool{runtime.WindowName(task.RootWindowName): true}
	for _, w := range t.Windows {
		known[runtime.WindowName(w.SessionName())] = true
	}
	var open []ExtraStatus
	for id, pane := range sess.Panes {
		name := runtime.DisplayName(id)
		if known[id] || !task.IsExtraWindow(name) || runtime.WindowName(name) != id {
			continue
		}
		state := RuntimeRunning
		if pane.Dead {
			state = RuntimeDead
		}
		open = append(open, ExtraStatus{Name: name, Running: pane.Command, Runtime: state})
	}
	sort.Slice(open, func(i, j int) bool { return open[i].Name < open[j].Name })
	return append(out, open...)
}

func (a *Aggregator) repoStatus(ctx context.Context, taskName, repoName string, r task.Repo, sess runtime.SessionState, runtimeUnknown bool) RepoStatus {
	row := RepoStatus{
		Task:    taskName,
		Repo:    repoName,
		Branch:  r.Branch,
		Agent:   r.AgentLabel(),
		Runtime: a.windowState(sess, repoName, runtimeUnknown),
		Git:     GitClean,
	}

	defer a.gitSlot(ctx)()
	st, err := a.Git.WorktreeStatus(ctx, r.Worktree)
	switch {
	case err != nil:
		row.Git = GitError
		row.Err = err
	case !st.Exists:
		row.Git = GitMissing
	default:
		row.Detail = st
		if st.Dirty() {
			row.Git = GitDirty
		} else {
			row.Git = GitClean
		}
		if st.Branch != "" {
			row.Branch = st.Branch
		}
		if st.Upstream == "" && st.Branch != "" && r.Base != "" {
			// A base that no longer resolves leaves the count unknown, which
			// is shown as nothing rather than as an error on a healthy repo.
			if n, err := a.Git.CountCommits(ctx, r.Worktree, r.Base, "HEAD"); err == nil {
				row.Unpushed = n
			}
		}
	}
	return row
}

// windowState is what the session says about one window.
func (a *Aggregator) windowState(sess runtime.SessionState, window string, runtimeUnknown bool) RuntimeState {
	switch {
	case runtimeUnknown:
		return RuntimeUnknown
	case !sess.Exists:
		return RuntimeNoSession
	}
	pane, ok := sess.Pane(window)
	switch {
	case !ok:
		return RuntimeNoWindow
	case pane.Dead:
		return RuntimeDead
	default:
		return RuntimeRunning
	}
}

// livePane is the multiplexer's view of a window whose process is alive, or
// the zero PaneState: a dead window's bell and output are history.
func (a *Aggregator) livePane(sess runtime.SessionState, window string, runtimeUnknown bool) runtime.PaneState {
	if runtimeUnknown || !sess.Exists {
		return runtime.PaneState{}
	}
	pane, ok := sess.Pane(window)
	if !ok || pane.Dead {
		return runtime.PaneState{}
	}
	return pane
}

// All reports the status of every task, sorted by name.
func (a *Aggregator) All(ctx context.Context, tasks []*task.Task) []TaskStatus {
	out := make([]TaskStatus, len(tasks))
	published := a.published(ctx)
	known := a.sessions(ctx)

	var wg sync.WaitGroup
	for i, t := range tasks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[i] = a.task(ctx, t, published, known)
		}()
	}
	wg.Wait()

	sort.Slice(out, func(i, j int) bool { return out[i].Task < out[j].Task })
	return out
}
