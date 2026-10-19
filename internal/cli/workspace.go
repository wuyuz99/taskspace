package cli

import (
	"slices"

	"github.com/wuyuz99/taskspace/internal/task"
	"github.com/wuyuz99/taskspace/internal/workspace"
)

// workspace is the lifecycle service over this App's store, git and runtime,
// reporting to its output.
func (a *App) workspace() *workspace.Service {
	return &workspace.Service{
		Store:    a.Store,
		Git:      a.Git,
		Runtime:  a.Runtime,
		Settle:   a.settle,
		Stdout:   a.Out,
		Stderr:   a.Err,
		Progress: a.render,
		NoWait:   a.noWait,
		Waiting: func(name string, h task.LockHolder) {
			a.warnf("tspace: waiting for %s, which is changing %s (Ctrl-C to stop waiting)\n", h, name)
		},
	}
}

// render shows what a lifecycle operation did, as it does it.
func (a *App) render(e workspace.Event) {
	switch e := e.(type) {
	case workspace.Warning:
		a.warnf("%s\n", e.Text)
	case workspace.WorktreeReady:
		res := e.Result
		if res.FetchErr != nil {
			a.warnf("%-12s could not fetch, using the refs already here: %v\n", e.Repo, res.FetchErr)
		}
		switch {
		case res.Tracking != "":
			a.printf("%-12s created worktree on %s (tracking %s)\n", e.Repo, e.Branch, res.Tracking)
		case res.Created && res.Base != "":
			a.printf("%-12s created worktree on new branch %s (from %s)\n", e.Repo, e.Branch, res.Base)
		case res.Created:
			a.printf("%-12s created worktree on existing branch %s\n", e.Repo, e.Branch)
		default:
			a.printf("%-12s worktree ready\n", e.Repo)
		}
	case workspace.HookStarted:
		a.printf("%-12s post_create: %s\n", e.Repo, e.Command)
	case workspace.HookFailed:
		a.warnf("%-12s post_create failed: %v\n", e.Repo, e.Err)
	case workspace.Launching:
		t := e.Task
		if e.Root {
			a.printf("%-12s %s at %s\n", "(root)", t.AgentLabel(), a.Store.TaskDir(t.Name))
		}
		if t.AgentRepo != "" && slices.Contains(e.Repos, t.AgentRepo) {
			a.printf("%-12s runs the task's agent: %s\n", t.AgentRepo, agentDisplay(task.Repo{Agent: t.RepoAgent(t.AgentRepo)}))
		}
	case workspace.WindowExited:
		if e.Window == task.RootWindowName {
			a.warnf("the root window has exited; inspect its output, then run: tspace restart %s --root\n", e.Task)
		} else {
			a.warnf("%s has exited; inspect its output, then run: tspace restart %s %s\n", e.Window, e.Task, e.Window)
		}
	case workspace.NoRepositories:
		a.printf("task %s has no repositories\n", e.Task)
	case workspace.SessionStopped:
		a.printf("stopped session %s\n", e.Session)
	case workspace.WorktreeRemoved:
		if e.Present {
			a.printf("%-12s removed worktree\n", e.Repo)
		} else {
			a.printf("%-12s no worktree\n", e.Repo)
		}
	case workspace.BranchKept:
		a.warnf("%-12s kept branch %s (%s)\n", e.Repo, e.Branch, e.Reason)
		if e.Orphaned {
			a.warnf("%-12s no task will refer to it once the record is gone; it is in %s\n", "", e.Path)
		}
	case workspace.BranchNotDeleted:
		a.warnf("%-12s could not delete branch %s: %v\n", e.Repo, e.Branch, e.Err)
	case workspace.BranchDeleted:
		a.printf("%-12s deleted branch %s (%s)\n", e.Repo, e.Branch, e.Reason)
	case workspace.Cleaned:
		if e.Removing {
			a.printf("\ncleaned %s\n", e.Task)
		} else {
			a.printf("\ncleaned %s; the task record is still there (tspace task rm %s to remove it)\n", e.Task, e.Task)
		}
	case workspace.RecordRemoved:
		a.printf("removed task %s\n", e.Task)
	case workspace.NotesRemoved:
		a.printf("deleted its notes (%s)\n", joinNames(e.Files))
	}
}
