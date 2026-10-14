package cli

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/wuyuz99/taskspace/internal/status"
	"github.com/wuyuz99/taskspace/internal/task"
)

// statusJSON is one task in `tspace status --json`.
type statusJSON struct {
	Task         string           `json:"task"`
	Session      string           `json:"session"`
	Running      bool             `json:"running"`
	RuntimeError string           `json:"runtime_error,omitempty"`
	Root         statusRootJSON   `json:"root"`
	Repos        []statusRepoJSON `json:"repos"`
}

type statusRootJSON struct {
	Agent   string       `json:"agent"`
	Runtime string       `json:"runtime"`
	Signal  *task.Signal `json:"signal,omitempty"`
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
	// Signal is what the agent last reported with `tspace signal`.
	Signal *task.Signal `json:"signal,omitempty"`
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
			Root:    statusRootJSON{Agent: ts.Root.Agent, Runtime: string(ts.Root.Runtime), Signal: ts.Root.Signal},
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
				Signal:     r.Signal,
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

// aggregator is the one place a status query is assembled, so every view of a
// task sees the same signals.
func (a *App) aggregator() *status.Aggregator {
	agg := status.New(a.Git, a.Runtime)
	agg.Signals = a.Store
	return agg
}

// statusTasks resolves the optional TASK argument to the tasks to report on.
func (a *App) statusTasks(args []string) ([]*task.Task, error) {
	if len(args) == 1 {
		t, err := a.loadTask(args[0])
		if err != nil {
			return nil, err
		}
		return []*task.Task{t}, nil
	}
	all, err := a.Store.List()
	if err := a.listed(err); err != nil {
		return nil, err
	}
	return all, nil
}

func (a *App) newStatusCmd() *cobra.Command {
	var asJSON, watch bool
	var interval time.Duration
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
			"  dead        the window is there but its command has exited; see tspace restart\n" +
			"  no-window   the session is up without a window for this row\n" +
			"  no-session  nothing is running for the task; see tspace start\n" +
			"  unknown     tmux could not be queried; the reason is printed below\n\n" +
			"GIT is missing (no worktree yet; see tspace start), error, clean or dirty,\n" +
			"followed by stashes, commits ahead of and behind the upstream, and commits\n" +
			"not pushed anywhere.\n\n" +
			"A SIGNAL column appears once an agent has reported on itself with\n" +
			"`tspace signal`: working, blocked, done or failed, and how long ago. It is\n" +
			"the agent's word, not something tspace observed.\n\n" +
			"Without TASK, every task is shown. --watch redraws the table until\n" +
			"interrupted; `tspace dash` is the interactive version. --json prints the\n" +
			"same observations as an array of tasks, with the counts as separate\n" +
			"fields; there a worktree holding only a stash is dirty, as it is to\n" +
			"tspace clean.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if watch && asJSON {
				return fmt.Errorf("choose either --watch or --json")
			}
			if watch && interval < 200*time.Millisecond {
				return fmt.Errorf("--interval must be at least 200ms")
			}
			ctx := cmd.Context()
			for {
				// Tasks are re-read on every pass, so a watch picks up tasks
				// created and removed while it runs.
				tasks, err := a.statusTasks(args)
				if err != nil {
					return err
				}
				results := a.aggregator().All(ctx, tasks)
				if asJSON {
					return a.printStatusJSON(tasks, results)
				}
				if !watch {
					printStatusTable(a.Out, a.Err, results)
					return nil
				}

				// Drawn into a buffer and written at once, so the screen is
				// never seen half cleared.
				var frame bytes.Buffer
				if isTerminal(a.Out) {
					frame.WriteString("\033[H\033[2J")
				}
				fmt.Fprintf(&frame, "tspace status at %s, every %s; Ctrl-C to stop\n\n", time.Now().Format("15:04:05"), interval)
				printStatusTable(&frame, &frame, results)
				if _, err := a.Out.Write(frame.Bytes()); err != nil {
					return err
				}
				select {
				case <-ctx.Done():
					return nil
				case <-time.After(interval):
				}
			}
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print machine-readable JSON instead of the table")
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "redraw the table until interrupted")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "time between redraws with --watch")
	return cmd
}

// printStatusTable renders the table, then the reason behind any "error" or
// "unknown" cell: a bare label is not actionable.
func printStatusTable(out, errOut io.Writer, results []status.TaskStatus) {
	if len(results) == 0 {
		fmt.Fprintf(out, "no tasks yet; create one with: tspace task create NAME\n")
		return
	}

	// The column is there only when an agent has said something, so a setup
	// with no signals reads as it always has.
	signals := false
	for _, ts := range results {
		signals = signals || ts.Root.Signal != nil
		for _, r := range ts.Repos {
			signals = signals || r.Signal != nil
		}
	}
	now := time.Now()
	row := func(tw io.Writer, sig *task.Signal, cells ...string) {
		if signals {
			cells = append(cells, signalSummary(sig, now))
		}
		fmt.Fprintln(tw, strings.Join(cells, "\t"))
	}

	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	header := "TASK\tREPO\tBRANCH\tAGENT\tRUNTIME\tGIT"
	if signals {
		header += "\tSIGNAL"
	}
	fmt.Fprintln(tw, header)

	empty := true
	for _, ts := range results {
		// The root window has a process but no worktree, so it has no
		// branch or git state of its own; its repositories follow it.
		row(tw, ts.Root.Signal, ts.Task, "(root)", "-", ts.Root.Agent, string(ts.Root.Runtime), "-")
		for _, r := range ts.Repos {
			empty = false
			row(tw, r.Signal, r.Task, r.Repo, dash(r.Branch), r.Agent, string(r.Runtime), gitSummary(r))
		}
	}
	tw.Flush()
	if empty {
		fmt.Fprintf(out, "no repositories attached yet\n")
	}

	for _, ts := range results {
		if ts.RuntimeErr != nil {
			fmt.Fprintf(errOut, "\n%s: runtime could not be queried: %v\n", ts.Task, ts.RuntimeErr)
		}
		for _, r := range ts.Repos {
			if r.Err != nil {
				fmt.Fprintf(errOut, "\n%s/%s: %v\n", r.Task, r.Repo, r.Err)
			}
		}
	}
}

// signalSummary renders the SIGNAL column: the state and how long it has stood.
func signalSummary(s *task.Signal, now time.Time) string {
	if s == nil {
		return "-"
	}
	return string(s.State) + " " + age(now.Sub(s.At))
}

// age renders a duration to one unit, which is all a glance needs.
func age(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", max(int(d.Seconds()), 0))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
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
