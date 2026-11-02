package workspace

import (
	"context"
	"slices"
	"time"

	"github.com/wuyuz99/taskspace/internal/runtime"
	"github.com/wuyuz99/taskspace/internal/task"
)

// snapshotWait is how long Observe waits for a task's lock. Whoever holds it
// records what they leave running, so giving up loses nothing.
const snapshotWait = 100 * time.Millisecond

// running is what an interruption of st would bring back: the task's primary
// and saved windows that are alive. A dead window was not running. Saved
// windows come back with a primary one, so with none there is nothing.
func running(s *Service, t *task.Task, st runtime.SessionState) (task.Running, bool) {
	alive := func(window string) bool {
		p, ok := st.Pane(window)
		return ok && !p.Dead
	}
	r := task.Running{Root: t.HasRootWindow() && alive(task.RootWindowName)}
	for _, name := range t.RepoNames() {
		if alive(name) {
			r.Repos = append(r.Repos, name)
		}
	}
	if !r.Root && len(r.Repos) == 0 {
		return task.Running{}, false
	}
	for _, w := range t.Windows {
		if alive(w.SessionName()) {
			r.Windows = append(r.Windows, w.Name)
		}
	}
	return r, true
}

func sameRunning(a, b task.Running) bool {
	return a.Whole == b.Whole && a.Root == b.Root && slices.Equal(a.Repos, b.Repos) && slices.Equal(a.Windows, b.Windows)
}

// snapshot writes down what the task's session runs, under its lock. A
// session that is gone leaves the last snapshot, which is what an interruption
// brings back: only stop and clean, knowing it ended on purpose, clear it.
func (s *Service) snapshot(ctx context.Context, name string) {
	if s.Runtime == nil {
		return
	}
	st, err := s.Runtime.State(ctx, s.Runtime.SessionName(name))
	if err != nil || !st.Exists {
		return
	}
	t, err := s.Store.Load(name)
	if err != nil {
		return
	}
	want, ok := running(s, t, st)
	have, recorded := s.Store.Running(name)
	switch {
	case !ok && recorded:
		err = s.Store.ClearRunning(name)
	case ok && (!recorded || !sameRunning(want, have)):
		err = s.Store.MarkRunning(name, want)
	}
	if err != nil {
		s.warnf("tspace: %v", err)
	}
}

// Observe updates the snapshot from a session state someone read for another
// reason, such as status. That is how a window opened or exited since the last
// command gets noticed. Nothing is locked or written unless it changed, and st
// is read again under the lock: by then a stop may have ended the session, and
// writing st would make that look like an interruption.
func (s *Service) Observe(ctx context.Context, t *task.Task, st runtime.SessionState) {
	if s.Runtime == nil || !st.Exists {
		return
	}
	want, ok := running(s, t, st)
	have, recorded := s.Store.Running(t.Name)
	if ok == recorded && (!ok || sameRunning(want, have)) {
		return
	}
	wait, cancel := context.WithTimeout(ctx, snapshotWait)
	defer cancel()
	l, err := s.Store.Lock(wait, t.Name, s.command("tspace status"), nil)
	if err != nil {
		return
	}
	defer l.Unlock()
	s.snapshot(ctx, t.Name)
}
