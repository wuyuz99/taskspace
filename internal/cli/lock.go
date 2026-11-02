package cli

import (
	"github.com/spf13/cobra"
	"github.com/wuyuz99/taskspace/internal/task"
)

// lockTask takes the task's lock for cmd, then loads the task, so what cmd
// saves was read under the lock; see workspace.Service.Lock. The lock rides
// in cmd's context, so commands cmd runs in-process take it again for free.
func (a *App) lockTask(cmd *cobra.Command, name string, lifecycle bool) (*task.Task, func(), error) {
	name, err := a.resolveTask(name)
	if err != nil {
		return nil, nil, err
	}
	t, ctx, unlock, err := a.workspace().Lock(cmd.Context(), name, cmd.CommandPath(), lifecycle)
	if err != nil {
		return nil, nil, err
	}
	cmd.SetContext(ctx)
	return t, unlock, nil
}

// configFor reads the config for cmd. A command writing it holds the config's
// lock until unlock, so what it saves was read under the lock; one only
// showing it does not wait.
func (a *App) configFor(cmd *cobra.Command, writing bool) (c *task.Config, unlock func(), err error) {
	unlock = func() {}
	if writing {
		if unlock, err = a.Store.LockConfig(cmd.Context()); err != nil {
			return nil, nil, err
		}
	}
	if c, err = a.Store.LoadConfig(); err != nil {
		unlock()
		return nil, nil, err
	}
	return c, unlock, nil
}
