package workspace

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/wuyuz99/taskspace/internal/runtime"
	"github.com/wuyuz99/taskspace/internal/task"
)

// StopWindow closes only the named window: a primary one, or an extra one
// named with its leading '_'. The rest of the session stays open, and a
// saved extra window stays saved, for the next start to open again.
func (s *Service) StopWindow(ctx context.Context, name, repo string, root bool) error {
	t, ctx, unlock, err := s.Lock(ctx, name, s.command("tspace stop"), true)
	if err != nil {
		return err
	}
	defer unlock()
	window := repo
	switch {
	case root:
		if repo != "" {
			return fmt.Errorf("--root cannot be combined with a repository")
		}
		if !t.HasRootWindow() {
			return fmt.Errorf("task %s has no root window", t.Name)
		}
		window = task.RootWindowName
	case repo == task.RootWindowName:
		return fmt.Errorf("%s is the root window; stop it with: tspace stop %s --root", repo, t.Name)
	case strings.HasPrefix(repo, task.ExtraWindowPrefix):
		// Repository names start with a letter or digit, so this can only be
		// an extra window, and it has to be open to be closed.
		if !task.IsExtraWindow(repo) {
			return fmt.Errorf("%q is not a window name tspace gives", repo)
		}
		st, err := s.Runtime.State(ctx, s.Runtime.SessionName(t.Name))
		if err != nil {
			return err
		}
		if _, open := st.Pane(s.Runtime.WindowName(repo)); !open {
			return fmt.Errorf("task %s has no open window %s", t.Name, repo)
		}
	default:
		if _, ok := t.Repo(repo); !ok {
			return fmt.Errorf("task %s has no repository %s", t.Name, repo)
		}
	}
	return s.closeWindow(ctx, t.Name, window)
}

func (s *Service) closeWindow(ctx context.Context, name, window string) error {
	rt, ok := s.Runtime.(runtime.WindowCloser)
	if !ok {
		return fmt.Errorf("this runtime does not support closing individual windows; stop the task first")
	}
	if err := rt.CloseWindow(ctx, s.Runtime.SessionName(name), window); err != nil {
		return err
	}
	// A multiplexer ends a session with its last window, and that was on
	// purpose too. A session that cannot be checked is taken as still up.
	if running, err := s.Runtime.HasSession(ctx, s.Runtime.SessionName(name)); err == nil && !running {
		s.markStopped(name)
	} else if !task.IsExtraWindow(window) {
		s.forgetRunning(name, window)
	}
	return s.Store.ClearSignal(name, window)
}

// forgetRunning records that window, a primary one, was closed on purpose,
// so an interruption does not bring it back. A failure is a warning, as for
// markStopped.
func (s *Service) forgetRunning(name, window string) {
	r, ok := s.Store.Running(name)
	if !ok {
		return
	}
	if r.Whole {
		t, err := s.Store.Load(name)
		if err != nil {
			s.warnf("tspace: %v", err)
			return
		}
		r = task.Running{Root: t.HasRootWindow(), Repos: t.RepoNames()}
	}
	if window == task.RootWindowName {
		r.Root = false
	} else {
		r.Repos = slices.DeleteFunc(r.Repos, func(repo string) bool { return repo == window })
	}
	// Only extra windows left: nothing an interruption would bring back.
	if !r.Root && len(r.Repos) == 0 {
		s.markStopped(name)
		return
	}
	if err := s.Store.MarkRunning(name, r); err != nil {
		s.warnf("tspace: %v", err)
	}
}

// rememberRunning records that window, a primary one, runs again in a
// session that is up, as restart may bring back one stop closed.
func (s *Service) rememberRunning(name, window string) {
	r, ok := s.Store.Running(name)
	if !ok {
		return
	}
	add := task.Running{Repos: []string{window}}
	if window == task.RootWindowName {
		add = task.Running{Root: true}
	}
	if err := s.Store.MarkRunning(name, r.With(add)); err != nil {
		s.warnf("tspace: %v", err)
	}
}
