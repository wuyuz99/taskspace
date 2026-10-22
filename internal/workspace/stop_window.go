package workspace

import (
	"context"
	"fmt"

	"github.com/wuyuz99/taskspace/internal/runtime"
	"github.com/wuyuz99/taskspace/internal/task"
)

// StopWindow closes only the named primary window; extra windows stay open.
func (s *Service) StopWindow(ctx context.Context, name, repo string, root bool) error {
	t, ctx, unlock, err := s.Lock(ctx, name, s.command("tspace stop"), true)
	if err != nil {
		return err
	}
	defer unlock()
	window := repo
	if root {
		if repo != "" {
			return fmt.Errorf("--root cannot be combined with a repository")
		}
		if !t.HasRootWindow() {
			return fmt.Errorf("task %s has no root window", t.Name)
		}
		window = task.RootWindowName
	} else if _, ok := t.Repo(repo); !ok {
		return fmt.Errorf("task %s has no repository %s", t.Name, repo)
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
