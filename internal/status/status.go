// Package status aggregates git and runtime state into what aw shows the user.
//
// Every status query in aw funnels through Aggregator.Task. That is deliberate:
// when notifications arrive (agent finished, agent blocked, run failed), they
// attach here, at one place, rather than being sprinkled through the commands.
package status

import (
	"context"
	"fmt"
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

// GitSummary renders the git column.
func (r RepoStatus) GitSummary() string {
	switch r.Git {
	case GitMissing:
		return "missing"
	case GitError:
		return "error"
	}

	s := "clean"
	if n := r.Detail.DirtyCount(); n > 0 {
		s = fmt.Sprintf("dirty (%d file%s)", n, plural(n))
	}
	if r.Detail.Stashes > 0 {
		s += fmt.Sprintf(", %d stash%s", r.Detail.Stashes, stashPlural(r.Detail.Stashes))
	}
	if r.Detail.Ahead > 0 {
		s += fmt.Sprintf(", ahead %d", r.Detail.Ahead)
	}
	if r.Detail.Behind > 0 {
		s += fmt.Sprintf(", behind %d", r.Detail.Behind)
	}
	return s
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func stashPlural(n int) string {
	if n == 1 {
		return ""
	}
	return "es"
}

// TaskStatus is a task and its repositories.
type TaskStatus struct {
	Task    string
	Session string
	Running bool
	Repos   []RepoStatus
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
	if a.Runtime != nil {
		out.Session = a.Runtime.SessionName(t.Name)
		if st, err := a.Runtime.State(ctx, out.Session); err == nil {
			sess = st
			out.Running = st.Exists
		} else {
			sess = runtime.SessionState{Panes: map[string]runtime.PaneState{}}
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
			rows[i] = a.repoStatus(ctx, t.Name, name, repo, sess)
		}()
	}
	wg.Wait()

	out.Repos = rows
	return out
}

func (a *Aggregator) repoStatus(ctx context.Context, taskName, repoName string, r task.Repo, sess runtime.SessionState) RepoStatus {
	row := RepoStatus{
		Task:    taskName,
		Repo:    repoName,
		Branch:  r.Branch,
		Agent:   r.AgentLabel(),
		Runtime: RuntimeNoSession,
		Git:     GitClean,
	}

	switch {
	case a.Runtime == nil:
		row.Runtime = RuntimeUnknown
	case !sess.Exists:
		row.Runtime = RuntimeNoSession
	default:
		if pane, ok := sess.Pane(sanitizeWindow(repoName)); !ok {
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

// sanitizeWindow mirrors the runtime's window naming so status can look up the
// pane belonging to a repo.
func sanitizeWindow(repo string) string { return runtime.WindowName(repo) }

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
