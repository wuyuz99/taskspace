package workspace

import (
	"context"
	"os"

	"github.com/wuyuz99/taskspace/internal/task"
)

// SettleLayout decides where the task's agent runs once repositories have been
// added, first if the task had none: in a sole repository's worktree, when
// nothing has run in a root window yet, and with a root window again once
// there is a second. It reports whether that root window should be opened now,
// in the running session.
func (s *Service) SettleLayout(ctx context.Context, t *task.Task, first bool) (openRoot bool) {
	movable := first && len(t.Repos) == 1 && t.HasRootWindow() && !t.RootWindow && t.CanRunAgentIn(t.RepoNames()[0])
	needsRoot := !t.HasRootWindow() && len(t.Repos) > 1
	if !movable && !needsRoot {
		return false
	}
	// A session that cannot be checked may be running the agent in the root
	// window: the agent is not moved out of it, and a new root window is left
	// for the next start to open.
	running, err := s.SessionUp(ctx, t.Name)
	if err != nil {
		if movable {
			s.warnf("tspace: %v; the task's agent stays in the root window", err)
		} else {
			s.warnf("tspace: %v; if it is running, the root window opens when the task next starts", err)
		}
	}
	switch {
	case movable && err == nil && !running:
		// Nothing has run in a root window that the agent could be moved out
		// of, so this task starts as a single-repo one.
		t.AgentRepo = t.RepoNames()[0]
		s.report(AgentPlaced{Task: t.Name, Repo: t.AgentRepo, Command: t.RepoAgent(t.AgentRepo)})
	case needsRoot:
		t.RootWindow = true
		openRoot = running
		if !running {
			s.report(RootWindowPending{Task: t.Name, AgentRepo: t.AgentRepo})
		}
	}
	return openRoot
}

// OpenRootWindow adds the task-root window to a running session, for a task
// that just gained one. Failing is only a warning: the record says the
// window exists, so the next start creates it.
func (s *Service) OpenRootWindow(ctx context.Context, t *task.Task) {
	w := RootWindow(s.Store, t)
	err := os.MkdirAll(w.Dir, 0o755)
	if err == nil {
		err = s.Runtime.OpenWindow(ctx, s.Runtime.SessionName(t.Name), w)
	}
	if err != nil {
		s.warnf("could not open the root window now (%v); the next tspace start %s will", err, t.Name)
		return
	}
	s.report(RootWindowOpened{Task: t.Name, Dir: w.Dir, AgentRepo: t.AgentRepo})
}
