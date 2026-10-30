package cli

import (
	"github.com/spf13/cobra"
	"github.com/wuyuz99/taskspace/internal/workspace"
)

func (a *App) newTaskRenameCmd() *cobra.Command {
	var branches bool
	cmd := &cobra.Command{
		Use:   "rename OLD NEW [--branches]",
		Short: "Rename a stopped task and move its worktrees and notes",
		Long: "Rename a stopped task, preserving uncommitted files, notes and settings.\n" +
			"Run from outside its directory. Branch names are kept unless --branches is\n" +
			"set; then only unpublished branches matching the configured task prefix\n" +
			"are renamed. Custom and published branches are reported and kept.\n\n" +
			"Agents that resume by directory leave their conversation at the old path,\n" +
			"and a note names each affected agent's old directory. An agent chosen with\n" +
			"`tspace config carry-conversations` has its history moved along instead.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.workspace().Rename(cmd.Context(), args[0], args[1], workspace.RenameOptions{Branches: branches}); err != nil {
				return err
			}
			a.printf("renamed %s to %s\n", args[0], args[1])
			return nil
		},
	}
	cmd.Flags().BoolVar(&branches, "branches", false, "rename unpublished branches matching the configured task prefix")
	return cmd
}
