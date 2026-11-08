package workspace

import (
	"context"
	"fmt"

	"github.com/wuyuz99/taskspace/internal/task"
)

// StopWindow closes only the named window, as task.KindOf reads its name. The
// rest of the session stays open, and a saved extra window stays saved, for
// the next start to open again.
func (s *Service) StopWindow(ctx context.Context, name, window string) error {
	t, ctx, unlock, err := s.Lock(ctx, name, s.command("tspace stop"), true)
	if err != nil {
		return err
	}
	defer unlock()
	switch task.KindOf(window) {
	case task.RootWindow:
		if !t.HasRootWindow() {
			return fmt.Errorf("task %s has no root window", t.Name)
		}
	case task.ExtraWindow:
		// It has to be open to be closed.
		if !task.IsExtraWindow(window) {
			return fmt.Errorf("%q is not a window name tspace gives", window)
		}
		st, err := s.Runtime.State(ctx, s.Runtime.SessionName(t.Name))
		if err != nil {
			return err
		}
		if _, open := st.Pane(window); !open {
			return fmt.Errorf("task %s has no open window %s", t.Name, window)
		}
	default:
		r, ok := t.Repo(window)
		if !ok {
			return fmt.Errorf("task %s has no repository %s", t.Name, window)
		}
		if r.Reference {
			return task.NoWindowError(window)
		}
	}
	return s.closeWindow(ctx, t.Name, window)
}

func (s *Service) closeWindow(ctx context.Context, name, window string) error {
	if err := s.Runtime.CloseWindow(ctx, s.Runtime.SessionName(name), window); err != nil {
		return err
	}
	// A multiplexer ends a session with its last window, and that was on
	// purpose too. A session that cannot be checked is taken as still up.
	if running, err := s.Runtime.HasSession(ctx, s.Runtime.SessionName(name)); err == nil && !running {
		s.markStopped(name)
	} else if task.IsExtraWindow(window) {
		if err := s.Store.ForgetWindow(name, window); err != nil {
			s.warnf("tspace: %v", err)
		}
	}
	return s.Store.ClearSignal(name, window)
}
