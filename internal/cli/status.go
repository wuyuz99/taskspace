package cli

import (
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/wuyuz99/taskspace/internal/status"
	"github.com/wuyuz99/taskspace/internal/task"
)

// statusJSON is one task in `aw status --json`.
type statusJSON struct {
	Task         string           `json:"task"`
	Session      string           `json:"session"`
	Running      bool             `json:"running"`
	RuntimeError string           `json:"runtime_error,omitempty"`
	Root         statusRootJSON   `json:"root"`
	Repos        []statusRepoJSON `json:"repos"`
}

type statusRootJSON struct {
	Agent   string `json:"agent"`
	Runtime string `json:"runtime"`
}

type statusRepoJSON struct {
	Repo       string `json:"repo"`
	Branch     string `json:"branch"`
	Worktree   string `json:"worktree"`
	Agent      string `json:"agent"`
	Runtime    string `json:"runtime"`
	Git        string `json:"git"`
	Upstream   string `json:"upstream,omitempty"`
	Staged     int    `json:"staged"`
	Modified   int    `json:"modified"`
	Untracked  int    `json:"untracked"`
	Conflicted int    `json:"conflicted"`
	Stashes    int    `json:"stashes"`
	Ahead      int    `json:"ahead"`
	Behind     int    `json:"behind"`
	Unpushed   int    `json:"unpushed"`
	Error      string `json:"error,omitempty"`
}

func (a *App) printStatusJSON(tasks []*task.Task, results []status.TaskStatus) error {
	byName := make(map[string]*task.Task, len(tasks))
	for _, t := range tasks {
		byName[t.Name] = t
	}
	out := make([]statusJSON, 0, len(results))
	for _, ts := range results {
		row := statusJSON{
			Task:    ts.Task,
			Session: ts.Session,
			Running: ts.Running,
			Root:    statusRootJSON{Agent: ts.Root.Agent, Runtime: string(ts.Root.Runtime)},
			Repos:   make([]statusRepoJSON, 0, len(ts.Repos)),
		}
		if ts.RuntimeErr != nil {
			row.RuntimeError = ts.RuntimeErr.Error()
		}
		for _, r := range ts.Repos {
			repo := statusRepoJSON{
				Repo:       r.Repo,
				Branch:     r.Branch,
				Worktree:   byName[ts.Task].Repos[r.Repo].Worktree,
				Agent:      r.Agent,
				Runtime:    string(r.Runtime),
				Git:        string(r.Git),
				Upstream:   r.Detail.Upstream,
				Staged:     r.Detail.Staged,
				Modified:   r.Detail.Modified,
				Untracked:  r.Detail.Untracked,
				Conflicted: r.Detail.Conflicted,
				Stashes:    r.Detail.Stashes,
				Ahead:      r.Detail.Ahead,
				Behind:     r.Detail.Behind,
				Unpushed:   r.Unpushed,
			}
			if r.Err != nil {
				repo.Error = r.Err.Error()
			}
			row.Repos = append(row.Repos, repo)
		}
		out = append(out, row)
	}
	return a.printJSON(out)
}

func (a *App) newStatusCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "status [TASK]",
		Short: "Show task, worktree and runtime state",
		Long: "status reports two independent things per repository: whether its agent\n" +
			"is running, and what git thinks of its worktree. They are separate\n" +
			"columns because a worktree can be dirty and running at the same time —\n" +
			"that is the normal state of an agent doing its job.\n\n" +
			"Each task's first row, (root), is the window at the task root. It has a\n" +
			"runtime state but no git state: its changes appear in the repositories.\n\n" +
			"RUNTIME is one of:\n\n" +
			"  running     the window's command is alive\n" +
			"  dead        the window is there but its command has exited; see aw restart\n" +
			"  no-window   the session is up without a window for this row\n" +
			"  no-session  nothing is running for the task; see aw start\n" +
			"  unknown     tmux could not be queried; the reason is printed below\n\n" +
			"GIT is missing (no worktree yet; see aw start), error, clean or dirty,\n" +
			"followed by stashes, commits ahead of and behind the upstream, and commits\n" +
			"not pushed anywhere.\n\n" +
			"Without TASK, every task is shown. --json prints the same observations\n" +
			"as an array of tasks, with the counts as separate fields; there a\n" +
			"worktree holding only a stash is dirty, as it is to aw clean.",
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
				if err := a.listed(err); err != nil {
					return err
				}
				tasks = all
			}

			if len(tasks) == 0 && !asJSON {
				a.printf("no tasks yet; create one with: aw task create NAME\n")
				return nil
			}

			agg := status.New(a.Git, a.Runtime)
			results := agg.All(cmd.Context(), tasks)
			if asJSON {
				return a.printStatusJSON(tasks, results)
			}

			tw := tabwriter.NewWriter(a.Out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "TASK\tREPO\tBRANCH\tAGENT\tRUNTIME\tGIT")

			empty := true
			for _, ts := range results {
				// The root window has a process but no worktree, so it has no
				// branch or git state of its own; its repositories follow it.
				fmt.Fprintf(tw, "%s\t(root)\t-\t%s\t%s\t-\n", ts.Task, ts.Root.Agent, ts.Root.Runtime)
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
	cmd.Flags().BoolVar(&asJSON, "json", false, "print machine-readable JSON instead of the table")
	return cmd
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
	if r.Unpushed > 0 {
		s += fmt.Sprintf(", %d commit%s not pushed", r.Unpushed, plural(r.Unpushed))
	}
	return s
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
