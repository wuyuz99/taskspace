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

	awgit "github.com/aw-tool/aw/internal/git"
	"github.com/aw-tool/aw/internal/runtime"
	"github.com/aw-tool/aw/internal/task"
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
	// Err is the error behind GitError, if any.
	Err error
}

// TaskStatus is a task and its repositories.
type TaskStatus struct {
	Task    string
	Session string
	Running bool
	Repos   []RepoStatus
	// RuntimeErr is set when the runtime could not be queried at all.
	RuntimeErr error
}

// Aggregator collects status across git and the runtime.
type Aggregator struct {
	Git     *awgit.Git
	Runtime runtime.Runtime
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
		Runtime: RuntimeNoSession,
		Git:     GitClean,
	}

	switch {
	case runtimeUnknown:
		row.Runtime = RuntimeUnknown
	case !sess.Exists:
		row.Runtime = RuntimeNoSession
	default:
		if pane, ok := sess.Pane(a.Runtime.WindowName(repoName)); !ok {
			row.Runtime = RuntimeNoWindow
		} else if pane.Dead {
			row.Runtime = RuntimeDead
		} else {
			row.Runtime = RuntimeRunning
		}
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
	}
	return row
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
