package cli

import (
	"fmt"
	"text/tabwriter"

	"github.com/aw-tool/aw/internal/status"
	"github.com/aw-tool/aw/internal/task"
	"github.com/spf13/cobra"
)

func (a *App) newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status [TASK]",
		Short: "Show task, worktree and runtime state",
		Long: "status reports two independent things per repository: whether its agent\n" +
			"is running, and what git thinks of its worktree. They are separate\n" +
			"columns because a worktree can be dirty and running at the same time —\n" +
			"that is the normal state of an agent doing its job.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var tasks []*task.Task

			if len(args) == 1 {
				t, err := a.loadTask(args[0])
				if err != nil {
					return err
				}
				tasks = []*task.Task{t}
			} else {
				all, err := a.Store.List()
				if err != nil {
					return err
				}
				tasks = all
			}

			if len(tasks) == 0 {
				a.printf("no tasks yet; create one with: aw task create NAME\n")
				return nil
			}

			agg := status.New(a.Git, a.Runtime)
			results := agg.All(cmd.Context(), tasks)

			tw := tabwriter.NewWriter(a.Out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "TASK\tREPO\tBRANCH\tAGENT\tRUNTIME\tGIT")

			empty := true
			for _, ts := range results {
				if len(ts.Repos) == 0 {
					fmt.Fprintf(tw, "%s\t-\t-\t-\t%s\t-\n", ts.Task, runtimeLabel(ts))
					empty = false
					continue
				}
				for _, r := range ts.Repos {
					empty = false
					fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
						r.Task, r.Repo, dash(r.Branch), r.Agent, r.Runtime, r.GitSummary())
				}
			}
			if err := tw.Flush(); err != nil {
				return err
			}
			if empty {
				a.printf("no repositories attached yet\n")
			}

			// Surface the reason behind any "error" cell; a bare label is not
			// actionable.
			for _, ts := range results {
				for _, r := range ts.Repos {
					if r.Err != nil {
						a.warnf("\n%s/%s: %v\n", r.Task, r.Repo, r.Err)
					}
				}
			}
			return nil
		},
	}
}

func runtimeLabel(ts status.TaskStatus) string {
	if ts.Running {
		return string(status.RuntimeRunning)
	}
	return string(status.RuntimeNoSession)
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
