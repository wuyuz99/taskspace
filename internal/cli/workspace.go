package cli

import (
	"fmt"
	"slices"

	"github.com/spf13/cobra"
	"github.com/wuyuz99/taskspace/internal/task"
	"github.com/wuyuz99/taskspace/internal/workspace"
)

// workspaceFor is the lifecycle service for cmd, which it names to anyone
// who has to wait for it.
func (a *App) workspaceFor(cmd *cobra.Command) *workspace.Service {
	ws := a.workspace()
	ws.Command = cmd.CommandPath()
	return ws
}

// workspace is the lifecycle service over this App's store, git and runtime,
// reporting to its output.
func (a *App) workspace() *workspace.Service {
	stdout, stderr := a.Out, a.Err
	if a.hookOut != nil {
		stdout, stderr = a.hookOut, a.hookOut
	}
	return &workspace.Service{
		Store:    a.Store,
		Git:      a.Git,
		Runtime:  a.Runtime,
		Settle:   a.settle,
		Stdout:   stdout,
		Stderr:   stderr,
		Progress: a.render,
		NoWait:   a.noWait,
		Carriers: conversationCarriers,
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
	case workspace.StartingTask:
		a.printf("== %s\n", e.Task)
	case workspace.TaskStarted:
		a.printf("%s\n\n", startedText(e.Result))
	case workspace.TaskNotStarted:
		a.warnf("%s: %v\n\n", e.Task, e.Err)
	case workspace.Resumed:
		a.printf("%-12s resumed: %s\n", windowCell(e.Window), shellJoin(e.Command))
	case workspace.ResumeFailed:
		a.warnf("%-12s its resume command exited at once; started it fresh\n", windowCell(e.Window))
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
		a.printf("%-12s removed worktree\n", e.Repo)
	case workspace.NoWorktree:
		a.printf("%-12s no worktree\n", e.Repo)
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
		if len(e.Repos) > 0 {
			a.printf("\ncleaned %s in %s\n", joinNames(e.Repos), e.Task)
		} else if e.Removing {
			a.printf("\ncleaned %s\n", e.Task)
		} else {
			a.printf("\ncleaned %s; the task record is still there (tspace task rm %s to remove it)\n", e.Task, e.Task)
		}
	case workspace.RecordRemoved:
		a.printf("removed task %s\n", e.Task)
	case workspace.ConversationCarried:
		a.printf("%-12s carried its %s conversation from %s\n", windowCell(e.Window), e.Agent, e.From)
	case workspace.NotesRemoved:
		a.printf("deleted its notes (%s)\n", joinNames(e.Files))
	}
}

// windowCell names a window in the first column of start's output.
func windowCell(window string) string {
	if window == task.RootWindowName {
		return "(root)"
	}
	return window
}

// The last line each lifecycle command prints, which dash shows too.

func startedText(res *workspace.StartResult) string {
	return fmt.Sprintf("session %s is up; attach with: tspace attach %s", res.Session, res.Task.Name)
}

func restartedText(label, taskName string, resumed bool) string {
	if resumed {
		return fmt.Sprintf("resumed %s; use tspace status %s to check its state", label, taskName)
	}
	return fmt.Sprintf("relaunched %s; use tspace status %s to check its state", label, taskName)
}

func stoppedText(taskName string, running bool) string {
	if !running {
		return "nothing running for " + taskName
	}
	return "stopped " + taskName + "; worktrees left in place"
}
