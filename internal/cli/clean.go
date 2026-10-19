package cli

import (
	"github.com/spf13/cobra"
	"github.com/wuyuz99/taskspace/internal/workspace"
)

func (a *App) newCleanCmd() *cobra.Command {
	var deleteBranch, force, rm bool

	cmd := &cobra.Command{
		Use:   "clean TASK",
		Short: "Remove a task's worktrees, refusing if work could be lost",
		Long: "clean stops the task's session and removes its worktrees.\n\n" +
			"It refuses outright if any worktree has uncommitted changes, untracked\n" +
			"files or stashes: refusing is always better than losing code. Branches\n" +
			"are left alone unless --delete-branch, and even then only when their\n" +
			"work is merged into the base branch or pushed to an upstream.\n\n" +
			"The task record survives; use `tspace task rm` to remove that, or --rm to\n" +
			"remove it here once the worktrees are gone, finishing the task in one step.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.workspaceFor(cmd).Clean(cmd.Context(), args[0], workspace.CleanOptions{DeleteBranch: deleteBranch, Force: force, Remove: rm})
		},
	}
	cmd.Flags().BoolVar(&deleteBranch, "delete-branch", false,
		"also delete task branches whose work is merged or pushed")
	cmd.Flags().BoolVar(&force, "force", false,
		"discard uncommitted work and unmerged branches")
	cmd.Flags().BoolVar(&rm, "rm", false,
		"also delete the task record and its notes, as tspace task rm does")
	return cmd
}
