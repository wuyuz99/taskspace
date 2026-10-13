// Package status aggregates git and runtime state into what aw shows the user.
//
// Every status query in aw funnels through Aggregator.Task. That is deliberate:
// when notifications arrive (agent finished, agent blocked, run failed), they
// attach here, at one place, rather than being sprinkled through the commands.
package status

import (
	"context"

	"sort"
	"sync"

	awgit "github.com/wuyuz99/taskspace/internal/git"
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

// RepoStatus is one row of `aw status`.
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
	Detail awgit.Status
	// Unpushed counts the commits on a branch with no upstream that are not on
	// its base: work that exists only on this machine. Detail's ahead/behind
	// need an upstream, which a new task branch does not have until pushed.
	Unpushed int
	// Signal is what the window's agent last reported, if anything. It is the
	// agent's word, shown beside Runtime and never folded into it.
	Signal *task.Signal
	// Err is the error behind GitError, if any.
	Err error
}

// TaskStatus is a task and its repositories.
type TaskStatus struct {
	Task    string
	Session string
	Running bool
	// Root is the task-root window, which has a process but no worktree.
	Root  RootStatus
	Repos []RepoStatus
	// RuntimeErr is set when the runtime could not be queried at all.
	RuntimeErr error
}

// RootStatus is the `(root)` row of `aw status`.
type RootStatus struct {
	Agent   string
	Runtime RuntimeState
	Signal  *task.Signal
}

// SignalReader supplies what agents have reported about themselves.
type SignalReader interface {
	ReadSignals(taskName string) (map[string]task.Signal, error)
}

// Aggregator collects status across git and the runtime.
type Aggregator struct {
	Git     *awgit.Git
	Runtime runtime.Runtime
	// Signals is optional; without it no row carries a signal.
	Signals SignalReader
}

// New returns an Aggregator.
func New(g *awgit.Git, rt runtime.Runtime) *Aggregator {
	return &Aggregator{Git: g, Runtime: rt}
}

// Task reports the status of one task.
//
// Repositories are queried concurrently: `git status` across several large
// repositories is the dominant cost of this command, and they are independent.
func (a *Aggregator) Task(ctx context.Context, t *task.Task) TaskStatus {
	out := TaskStatus{Task: t.Name}

	var sess runtime.SessionState
	// A runtime we could not query is not the same as a runtime with nothing
	// running. Reporting a broken tmux server as "no-session" would tell the
	// user their agents are stopped when we simply do not know.
	runtimeUnknown := a.Runtime == nil
	if a.Runtime != nil {
		out.Session = a.Runtime.SessionName(t.Name)
		st, err := a.Runtime.State(ctx, out.Session)
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

	out.Root = RootStatus{
		Agent:   t.AgentLabel(),
		Runtime: a.windowState(sess, task.RootWindowName, runtimeUnknown),
		Signal:  signal(task.RootWindowName),
	}

	names := t.RepoNames()
	rows := make([]RepoStatus, len(names))

	var wg sync.WaitGroup
	for i, name := range names {
		i, name := i, name
		repo := t.Repos[name]
		wg.Add(1)
		go func() {
			defer wg.Done()
			rows[i] = a.repoStatus(ctx, t.Name, name, repo, sess, runtimeUnknown)
			rows[i].Signal = signal(name)
		}()
	}
	wg.Wait()

	out.Repos = rows
	return out
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
	pane, ok := sess.Pane(a.Runtime.WindowName(window))
	switch {
	case !ok:
		return RuntimeNoWindow
	case pane.Dead:
		return RuntimeDead
	default:
		return RuntimeRunning
	}
}

// All reports the status of every task, sorted by name.
func (a *Aggregator) All(ctx context.Context, tasks []*task.Task) []TaskStatus {
	out := make([]TaskStatus, len(tasks))

	var wg sync.WaitGroup
	for i, t := range tasks {
		i, t := i, t
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[i] = a.Task(ctx, t)
		}()
	}
	wg.Wait()

	sort.Slice(out, func(i, j int) bool { return out[i].Task < out[j].Task })
	return out
}
