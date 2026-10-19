package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/wuyuz99/taskspace/internal/task"
)

// lockTask takes the task's lock for cmd, then loads the task, so what cmd
// saves was read under the lock; task.Lock has the rules. The lock rides in
// cmd's context, so commands cmd runs in-process take it again for free.
//
// lifecycle is for commands that change worktrees or the session: they refuse
// a lock inherited from the tspace that runs them, such as a post_create hook
// running `tspace clean` on the task its start is creating.
func (a *App) lockTask(cmd *cobra.Command, name string, lifecycle bool) (*task.Task, func(), error) {
	if err := task.ValidateName("task", name); err != nil {
		return nil, nil, err
	}
	// No lock file for a name that is not a task.
	if !a.Store.Exists(name) {
		return nil, nil, fmt.Errorf("%q: %w", name, task.ErrNotFound)
	}
	l, err := a.acquire(cmd, name)
	if err != nil {
		return nil, nil, err
	}
	if lifecycle && l.Inherited() {
		l.Unlock()
		return nil, nil, fmt.Errorf("%s cannot run from a hook of the tspace that is already changing %s; it may only update the task's record", cmd.CommandPath(), name)
	}
	t, err := a.Store.Load(name)
	if err != nil {
		l.Unlock()
		return nil, nil, err
	}
	return t, l.Unlock, nil
}

// acquire takes name's lock and puts it in cmd's context, saying so when it
// has to wait.
func (a *App) acquire(cmd *cobra.Command, name string) (*task.Lock, error) {
	l, err := a.Store.Lock(cmd.Context(), name, cmd.CommandPath()+" "+name, func(h task.LockHolder) {
		a.warnf("tspace: waiting for %s, which is changing %s (Ctrl-C to stop waiting)\n", h, name)
	})
	if err != nil {
		return nil, err
	}
	cmd.SetContext(task.WithLock(cmd.Context(), l))
	return l, nil
}
