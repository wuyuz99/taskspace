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
						r.Task, r.Repo, dash(r.Branch), r.Agent, r.Runtime, gitSummary(r))
				}
			}
			if err := tw.Flush(); err != nil {
				return err
			}
			if empty {
				a.printf("no repositories attached yet\n")
			}

			// Surface the reason behind any "error" or "unknown" cell; a bare
			// label is not actionable.
			for _, ts := range results {
				if ts.RuntimeErr != nil {
					a.warnf("\n%s: runtime could not be queried: %v\n", ts.Task, ts.RuntimeErr)
				}
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

// gitSummary renders the GIT column.
//
// This lives in cli, not status: status reports observations, and turning them
// into English is presentation.
func gitSummary(r status.RepoStatus) string {
	switch r.Git {
	case status.GitMissing:
		return "missing"
	case status.GitError:
		return "error"
	}

	s := "clean"
	if n := r.Detail.DirtyCount(); n > 0 {
		s = fmt.Sprintf("dirty (%d file%s)", n, plural(n))
	}
	if r.Detail.Stashes > 0 {
		s += fmt.Sprintf(", %d stash%s", r.Detail.Stashes, stashPlural(r.Detail.Stashes))
	}
	if r.Detail.Ahead > 0 {
		s += fmt.Sprintf(", ahead %d", r.Detail.Ahead)
	}
	if r.Detail.Behind > 0 {
		s += fmt.Sprintf(", behind %d", r.Detail.Behind)
	}
	return s
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
