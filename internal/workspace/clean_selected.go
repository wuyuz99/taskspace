package workspace

import (
	"context"
	"fmt"

	"github.com/wuyuz99/taskspace/internal/task"
)

func (s *Service) stopSelectedForClean(ctx context.Context, t, selected *task.Task) error {
	running, err := s.Runtime.HasSession(ctx, s.Runtime.SessionName(t.Name))
	if err != nil {
		return err
	}
	if !running {
		return nil
	}
	st, err := s.Runtime.State(ctx, s.Runtime.SessionName(t.Name))
	if err != nil {
		return err
	}
	known := map[string]bool{task.RootWindowName: true}
	windows := selected.RepoNames()
	for _, name := range t.RepoNames() {
		known[s.Runtime.WindowName(name)] = true
	}
	for _, w := range t.Windows {
		known[s.Runtime.WindowName(w.SessionName())] = true
		if _, ok := selected.Repos[w.Repo]; ok {
			windows = append(windows, w.SessionName())
		}
	}
	// Ad-hoc windows have no recorded repository. Never remove a directory
	// from under one or guess ownership from a mutable process cwd.
	for name := range st.Panes {
		if !known[name] {
			return fmt.Errorf("window %s has no saved repository; stop the task before cleaning individual repositories, or save that window with its repository", name)
		}
	}
	for _, window := range windows {
		if _, exists := st.Panes[s.Runtime.WindowName(window)]; !exists {
			continue
		}
		if err := s.closeWindow(ctx, t.Name, window); err != nil {
			return err
		}
	}
	return nil
}
