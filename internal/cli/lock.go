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

// acquire takes name's lock, for a task that need not exist yet, and puts it
// in cmd's context.
func (a *App) acquire(cmd *cobra.Command, name string) (*task.Lock, error) {
	ctx, l, err := a.workspace().Acquire(cmd.Context(), name, cmd.CommandPath())
	if err != nil {
		return nil, err
	}
	cmd.SetContext(ctx)
	return l, nil
}
