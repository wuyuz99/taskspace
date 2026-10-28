package workspace

import (
	"context"
	"fmt"
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
	return s.Store.ClearSignal(name, window)
}
