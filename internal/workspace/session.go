package workspace

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/wuyuz99/taskspace/internal/runtime"
	"github.com/wuyuz99/taskspace/internal/task"
)

// Stop ends the task's session, interrupting its agents first, and leaves
// worktrees and branches alone. It reports whether there was a session.
func (s *Service) Stop(ctx context.Context, name string) (running bool, err error) {
	t, ctx, unlock, err := s.Lock(ctx, name, s.command("tspace stop"), true)
	if err != nil {
		return false, err
	}
	defer unlock()
	session := s.Runtime.SessionName(t.Name)

	running, err = s.Runtime.HasSession(ctx, session)
	if err != nil {
		return false, err
	}
	if !running {
		_ = s.Store.ClearSignals(t.Name)
		return false, nil
	}
	if err := s.Runtime.Stop(ctx, session); err != nil {
		return true, err
	}
	if err := s.Store.ClearSignals(t.Name); err != nil {
		s.warnf("tspace: %v", err)
	}
	return true, nil
}

// RestartOptions choose the window Restart relaunches, and how.
type RestartOptions struct {
	// Root is the task-root window; otherwise Repo's.
	Root bool
	Repo string
	// Resume runs the window's resume command, which brings the agent back
	// with its conversation, instead of its command, and refuses a window
	// without one. Fresh runs its command. With neither, a window with a
	// resume command is resumed, and relaunched fresh if that exits at once,
	// as Start does.
	Resume, Fresh bool
}

// Restart relaunches a dead window. It returns the window's label, such as
// "PAY-1/backend", and whether its agent was resumed.
func (s *Service) Restart(ctx context.Context, name string, opts RestartOptions) (label string, resumed bool, err error) {
	t, ctx, unlock, err := s.Lock(ctx, name, s.command("tspace restart"), true)
	if err != nil {
		return "", false, err
	}
	defer unlock()
	if opts.Resume && opts.Fresh {
		return "", false, fmt.Errorf("choose either --resume or --fresh")
	}
	var w runtime.Window
	label = t.Name
	if opts.Root {
		if !t.HasRootWindow() {
			return "", false, fmt.Errorf("task %s has no root window; its agent runs in %s: tspace restart %s %s", t.Name, t.AgentRepo, t.Name, t.AgentRepo)
		}
		w = RootWindow(s.Store, t)
		if info, err := os.Stat(w.Dir); err != nil || !info.IsDir() {
			return "", false, fmt.Errorf("task directory %s is missing; run: tspace start %s", w.Dir, t.Name)
		}
		label += " root window"
	} else if opts.Repo == task.RootWindowName {
		return "", false, fmt.Errorf("%s is the root window; restart it with: tspace restart %s --root", opts.Repo, t.Name)
	} else if strings.HasPrefix(opts.Repo, task.ExtraWindowPrefix) {
		// Only a saved window's command is known; an unsaved one's was typed
		// once and never recorded.
		i, ok := t.SavedWindow(opts.Repo)
		if !ok {
			return "", false, fmt.Errorf("%s is not saved, so tspace does not know what it ran; close it with: tspace stop %s %s", opts.Repo, t.Name, opts.Repo)
		}
		if opts.Resume {
			return "", false, fmt.Errorf("%s is an extra window; there is no agent to resume", opts.Repo)
		}
		w = ExtraWindow(s.Store, t, t.Windows[i])
		if info, err := os.Stat(w.Dir); err != nil || !info.IsDir() {
			return "", false, fmt.Errorf("%s is missing; run: tspace start %s", w.Dir, t.Name)
		}
		label += "/" + w.Name
	} else {
		if opts.Repo == "" {
			return "", false, fmt.Errorf("name a repository of %s to restart, or its root window", t.Name)
		}
		_, repo, err := WorktreeDir(t, opts.Repo)
		if err != nil {
			return "", false, err
		}
		w = RepoWindow(s.Store, t, repo)
		label += "/" + repo
	}
	if opts.Resume {
		argv := t.ResumeFor(w.Name)
		switch {
		case len(argv) > 0:
		case len(t.WindowAgent(w.Name)) == 0:
			return "", false, fmt.Errorf("%s runs a shell; there is no agent to resume", label)
		default:
			return "", false, fmt.Errorf("%s has no resume command; set one with the agent's own, e.g. %s", label, ResumeHint(t, w.Name))
		}
		w.Command = ExpandArgv(s.Store, t, argv)
		resumed = true
	}
	fresh := w
	auto := false
	if argv := t.ResumeFor(w.Name); len(argv) > 0 && !opts.Resume && !opts.Fresh {
		w.Command = ExpandArgv(s.Store, t, argv)
		resumed, auto = true, true
	}
	if err := s.ensureContextDir(t); err != nil {
		return "", false, err
	}
	session := s.Runtime.SessionName(t.Name)
	if err := s.Runtime.Restart(ctx, session, w); err != nil {
		return "", false, err
	}
	// The relaunched agent has said nothing yet.
	if err := s.Store.ClearSignal(t.Name, w.Name); err != nil {
		s.warnf("tspace: %v", err)
	}
	// Resumed unasked, a window whose resume command finds nothing to resume
	// and exits is started fresh, as Start's are.
	if auto {
		relaunched, err := s.checkLaunched(ctx, t, session, runtime.SessionState{}, []runtime.Window{w}, []string{w.Name}, map[string]runtime.Window{w.Name: fresh})
		if err != nil {
			return "", false, err
		}
		resumed = len(relaunched) == 0
	}
	return label, resumed, nil
}

// ResumeHint is the command that would set a window's resume command.
func ResumeHint(t *task.Task, window string) string {
	if window == t.AgentWindow() {
		return "tspace task update " + t.Name + " --resume CMD --resume ARG..."
	}
	return "tspace repo update " + t.Name + " " + window + " --resume CMD --resume ARG..."
}
