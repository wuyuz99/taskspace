package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/wuyuz99/taskspace/internal/task"
)

func (a *App) newTaskCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "task",
		Short: "Create, list, inspect and remove tasks",
		Long: "A task is one piece of work and the repositories it spans. Its record\n" +
			"holds a name, an optional title and ticket, the agent for its root window\n" +
			"and its repositories; its brief and handoff are Markdown notes beside its\n" +
			"worktrees. Elsewhere tspace calls a task's directory its workspace.",
	}
	cmd.AddCommand(a.newTaskCreateCmd(), a.newTaskListCmd(), a.newTaskShowCmd(), a.newTaskRmCmd())
	cmd.AddCommand(a.newTaskUpdateCmd(), a.newTaskNoteCmd(false), a.newTaskNoteCmd(true))
	return cmd
}

func (a *App) newTaskCreateCmd() *cobra.Command {
	var repos []string
	var project string
	var details taskDetails
	var agent agentOptions
	var resume resumeOptions
	var withRoot, start bool

	cmd := &cobra.Command{
		Use:     "create NAME [REPO...]",
		Aliases: []string{"new"},
		Short:   "Create a task",
		Long: "create records a task and the repositories it spans. Each REPO is a name\n" +
			"registered with `tspace config repo` or a path, optionally with a branch and\n" +
			"a base, as for `tspace repo add`:\n\n" +
			"  tspace task create PAY-142 backend frontend@feature/x infra:develop\n\n" +
			"NAME starts with a letter or digit and holds only letters, digits, '.',\n" +
			"'_' and '-'. It names the task's directory, session and default branch.\n\n" +
			"The task's agent is the one from `tspace config agent`, or a shell. --agent\n" +
			"overrides it, one flag per word of the command:\n\n" +
			"  tspace task create PAY-142 backend --agent claude --agent=--model --agent opus\n\n" +
			"With several repositories, or none yet, the agent runs in a task-root window\n" +
			"that sees every worktree. With one, the session has no root window: the\n" +
			"agent runs in that repository's worktree, and keeps running there when\n" +
			"`tspace repo add` brings a second, which opens a root window with a shell.\n" +
			"--with-root keeps the task-root window for a single-repository task too.\n\n" +
			"Without --ticket, the ticket is found in NAME by the pattern from `tspace\n" +
			"config ticket-pattern`, if one is set.\n\n" +
			"--brief, or --brief-file for a Markdown file, stores the task's\n" +
			"instructions where its agents will look for them; see `tspace task\n" +
			"brief --help`.\n\n" +
			"No worktree is created until `tspace start` or `tspace open`; --start runs\n" +
			"`tspace start NAME` straight after, which needs no terminal.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			brief, err := details.readBrief(cmd)
			if err != nil {
				return err
			}
			if err := agent.validate(cmd); err != nil {
				return err
			}
			if err := resume.validate(cmd); err != nil {
				return err
			}
			name := args[0]
			if err := task.ValidateName("task", name); err != nil {
				return err
			}
			if a.Store.Exists(name) {
				return fmt.Errorf("%q: %w", name, task.ErrExists)
			}
			t := task.New(name)
			details.apply(cmd, t)
			// Loaded only when something needs it, so a task that names its
			// agent and no repositories can be created with a broken config.
			var cfg *task.Config
			config := func() (*task.Config, error) {
				if cfg == nil {
					c, err := a.Store.LoadConfig()
					if err != nil {
						return nil, err
					}
					cfg = c
				}
				return cfg, nil
			}
			// The root agent comes from --agent/--shell, else the recipe, else
			// the user's default. Whichever wins is recorded on the task.
			agentFrom := ""
			// Each repository's report is held until the record is created, so
			// a later repository that fails does not follow "added backend"
			// for a task that was never made.
			out := a.Out
			var added bytes.Buffer
			a.Out = &added
			defer func() { a.Out = out }()
			if cmd.Flags().Changed("project") {
				p, err := a.Store.LoadProject(project)
				if err != nil {
					return err
				}
				t.Agent = append([]string(nil), p.Agent...)
				t.Resume = append([]string(nil), p.Resume...)
				t.Windows = task.CopyWindows(p.Windows)
				if len(p.Agent) > 0 {
					agentFrom = "project " + p.Name
				}
				for _, repoName := range p.RepoNames() {
					r := p.Repos[repoName]
					c, err := config()
					if err != nil {
						return err
					}
					if err := a.addRepo(cmd.Context(), t, c, r.Path, addRepoOptions{Name: repoName, Base: r.Base, Agent: r.Agent, Resume: r.Resume, PostCreate: r.PostCreate}); err != nil {
						return err
					}
				}
			}

			// Each REPO is sugar for a following `tspace repo add`.
			for _, spec := range append(repos, args[1:]...) {
				c, err := config()
				if err != nil {
					return err
				}
				if err := a.addRepoSpec(cmd.Context(), t, c, spec, addRepoOptions{}); err != nil {
					return err
				}
			}
			a.Out = out
			// Like the agent, a broken config does not stop a task that needs
			// nothing from it.
			if !cmd.Flags().Changed("ticket") {
				c, err := config()
				if err == nil {
					t.Ticket, err = c.TicketFrom(name)
				}
				if err != nil {
					a.warnf("note: no ticket read from the name: %v\n", err)
				}
			}
			// A resume command goes with the agent it resumes: the recipe's or
			// the config's comes along only with that agent.
			switch {
			case agent.apply(cmd, t):
				agentFrom = ""
				t.Resume = nil
			case len(t.Agent) == 0:
				cfg, err := config()
				if err != nil {
					return err
				}
				if len(cfg.Agent) > 0 {
					t.Agent = append([]string(nil), cfg.Agent...)
					t.Resume = append([]string(nil), cfg.Resume...)
					agentFrom = "tspace config"
				}
			}
			t.Resume, _ = resume.apply(cmd, t.Resume, false)
			if len(t.Resume) > 0 && len(t.Agent) == 0 {
				return fmt.Errorf("--resume resumes the task's agent, and it has none; give one with --agent")
			}
			if withRoot {
				t.RootWindow = true
			} else if len(t.Repos) == 1 && t.CanRunAgentIn(t.RepoNames()[0]) {
				t.AgentRepo = t.RepoNames()[0]
			}
			if start && len(t.Repos) == 0 {
				return fmt.Errorf("--start needs a repository to start; name one, or add it later and run: tspace start %s", name)
			}
			// Held from here through --start. Publishing never replaces a
			// record, so of two creates at once only one gets this far.
			l, err := a.acquire(cmd, name)
			if err != nil {
				return err
			}
			defer l.Unlock()
			if err := a.Store.CreatePrepared(t); err != nil {
				return err
			}
			// The brief is written after the record exists, so a failure here
			// leaves a task without a brief rather than a note without a task.
			if err := a.applyBrief(cmd, t, brief); err != nil {
				return err
			}
			if _, err := added.WriteTo(a.Out); err != nil {
				return err
			}
			a.printf("created task %s\n", name)
			if len(t.Agent) > 0 {
				from := ""
				if agentFrom != "" {
					from = ", from " + agentFrom
				}
				where := "task root"
				if t.AgentRepo != "" {
					where = "in " + t.AgentRepo + "'s worktree"
				}
				a.printf("  agent: %s (%s%s)\n", shellJoin(t.Agent), where, from)
			}
			if start {
				a.printf("\n")
				if err := a.runCmd(cmd.Context(), (*App).newStartCmd, t.Name); err != nil {
					return fmt.Errorf("task %s was created, but starting it failed: %w", t.Name, err)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&repos, "repo", nil,
		"attach a repository, the same as a REPO argument (repeatable)")
	cmd.Flags().StringVar(&project, "project", "", "snapshot a saved project recipe into this task")
	resume.flags(cmd, true)
	cmd.Flags().BoolVar(&start, "start", false, "start the task once it is created, as tspace start NAME does")
	cmd.Flags().BoolVar(&withRoot, "with-root", false,
		"give a single-repository task a task-root window for its agent, as with several")
	details.flags(cmd)
	agent.flags(cmd)
	return cmd
}

// taskJSON is a task in `tspace task list --json` and `tspace task show --json`.
type taskJSON struct {
	Name             string         `json:"name"`
	Title            string         `json:"title,omitempty"`
	Ticket           string         `json:"ticket,omitempty"`
	TicketURL        string         `json:"ticket_url,omitempty"`
	Session          string         `json:"session"`
	Dir              string         `json:"dir"`
	ContextDir       string         `json:"context_dir"`
	Agent            []string       `json:"agent"`
	Resume           []string       `json:"resume"`
	AgentWindow      string         `json:"agent_window"`
	RootWindow       bool           `json:"root_window"`
	Brief            string         `json:"brief,omitempty"`
	Handoff          string         `json:"handoff,omitempty"`
	HandoffUpdatedAt *time.Time     `json:"handoff_updated_at,omitempty"`
	Repos            []taskRepoJSON `json:"repos"`
	// Windows are the saved extra windows; see `tspace window --save`.
	Windows []taskWindowJSON `json:"windows"`
}

type taskWindowJSON struct {
	// Name is the window's name in the session, with its leading '_'.
	Name string `json:"name"`
	// Repo is where it opens; empty, the task root.
	Repo    string   `json:"repo,omitempty"`
	Command []string `json:"command"`
}

type taskRepoJSON struct {
	Name        string   `json:"name"`
	Path        string   `json:"path"`
	Branch      string   `json:"branch"`
	Base        string   `json:"base,omitempty"`
	BaseAssumed bool     `json:"base_assumed,omitempty"`
	Worktree    string   `json:"worktree"`
	Agent       []string `json:"agent"`
	Resume      []string `json:"resume"`
	PostCreate  string   `json:"post_create,omitempty"`
}

// describeTask renders a task's record and notes. An empty agent is the shell.
func (a *App) describeTask(t *task.Task) taskJSON {
	out := taskJSON{
		Name:        t.Name,
		Title:       t.Title,
		Ticket:      t.Ticket,
		TicketURL:   t.TicketURL,
		Session:     a.Runtime.SessionName(t.Name),
		Dir:         a.Store.TaskDir(t.Name),
		ContextDir:  a.Store.ContextDir(t.Name),
		Agent:       append([]string{}, t.Agent...),
		Resume:      append([]string{}, t.Resume...),
		AgentWindow: t.AgentWindow(),
		RootWindow:  t.HasRootWindow(),
		Repos:       make([]taskRepoJSON, 0, len(t.Repos)),
		Windows:     make([]taskWindowJSON, 0, len(t.Windows)),
	}
	brief, handoff, err := a.notes(t)
	if err != nil {
		a.warnf("tspace: %s: %v\n", t.Name, err)
	}
	out.Brief, out.Handoff = brief.Text, handoff.Text
	if !handoff.UpdatedAt.IsZero() {
		at := handoff.UpdatedAt.UTC()
		out.HandoffUpdatedAt = &at
	}
	for _, name := range t.RepoNames() {
		r := t.Repos[name]
		out.Repos = append(out.Repos, taskRepoJSON{
			Name: name, Path: r.Path, Branch: r.Branch, Base: r.Base, Worktree: r.Worktree,
			BaseAssumed: r.BaseAssumed,
			Agent:       append([]string{}, t.RepoAgent(name)...), Resume: append([]string{}, t.ResumeFor(name)...), PostCreate: r.PostCreate,
		})
	}
	for _, w := range t.Windows {
		out.Windows = append(out.Windows, taskWindowJSON{Name: w.SessionName(), Repo: w.Repo, Command: append([]string{}, w.Command...)})
	}
	return out
}

func (a *App) newTaskListCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List tasks",
		Long: "list prints one row per task: its ticket and title, the first line of\n" +
			"its handoff with the time that was written, and its repositories.\n" +
			"It reads only the records; `tspace status` adds what is running and dirty.\n\n" +
			"--json prints an array of the objects `tspace task show --json` prints.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			tasks, err := a.Store.List()
			if err := a.listed(err); err != nil {
				return err
			}
			if asJSON {
				out := make([]taskJSON, 0, len(tasks))
				for _, t := range tasks {
					out = append(out, a.describeTask(t))
				}
				return a.printJSON(out)
			}
			if len(tasks) == 0 {
				a.printf("no tasks yet; create one with: tspace task create NAME\n")
				return nil
			}

			tw := tabwriter.NewWriter(a.Out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "TASK\tTICKET\tTITLE\tHANDOFF\tUPDATED (UTC)\tREPOS")
			for _, t := range tasks {
				_, handoff, err := a.notes(t)
				if err != nil {
					a.warnf("tspace: %s: %v\n", t.Name, err)
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", t.Name, dash(t.Ticket), dash(oneLine(t.Title)),
					dash(noteSummary(handoff.Text)), handoffTime(handoff), dash(joinNames(t.RepoNames())))
			}
			return tw.Flush()
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print machine-readable JSON instead of the table")
	return cmd
}

func (a *App) newTaskShowCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "show NAME",
		Short: "Show a task's repositories in detail",
		Long: "show prints everything recorded about a task: its session name, its\n" +
			"directory and .context directory, its root agent, title, ticket, brief\n" +
			"and handoff, and for each repository the source path, branch, base,\n" +
			"worktree path, agent and setup hook.\n\n" +
			"It reads only the record, so the paths are shown whether or not the\n" +
			"worktrees exist yet; `tspace status NAME` reports what is on disk and running.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			t, err := a.loadTask(args[0])
			if err != nil {
				return err
			}
			if asJSON {
				return a.printJSON(a.describeTask(t))
			}

			a.printf("task:     %s\n", t.Name)
			a.printf("session:  %s\n", a.Runtime.SessionName(t.Name))
			a.printf("workspace: %s\n", a.Store.TaskDir(t.Name))
			a.printf("context:   %s\n", a.Store.ContextDir(t.Name))
			if t.AgentRepo == "" {
				a.printf("agent:     %s (window %s, at the workspace root)\n", agentDisplay(task.Repo{Agent: t.Agent}), task.RootWindowName)
			} else {
				a.printf("agent:     %s (window %s, in its worktree)\n", agentDisplay(task.Repo{Agent: t.RepoAgent(t.AgentRepo)}), t.AgentRepo)
				if t.RootWindow {
					a.printf("root:      shell (window %s, at the workspace root)\n", task.RootWindowName)
				} else {
					a.printf("root:      none until a second repository is added\n")
				}
			}
			if len(t.Resume) > 0 {
				a.printf("resume:    %s\n", shellJoin(t.Resume))
			}
			for i, w := range t.Windows {
				label := "windows:  "
				if i > 0 {
					label = "          "
				}
				where, what := "at the root", "shell"
				if w.Repo != "" {
					where = "in " + w.Repo
				}
				if len(w.Command) > 0 {
					what = shellJoin(w.Command)
				}
				a.printf("%s %s %s: %s\n", label, w.SessionName(), where, what)
			}
			a.printTaskContext(t)
			if len(t.Repos) == 0 {
				a.printf("\nno repositories yet; add one with:\n  tspace repo add %s REPO\n", t.Name)
				return nil
			}

			for _, name := range t.RepoNames() {
				r := t.Repos[name]
				a.printf("\n%s\n", name)
				a.printf("  repo:      %s\n", r.Path)
				a.printf("  branch:    %s\n", r.Branch)
				if r.Base != "" {
					a.printf("  base:      %s\n", r.Base)
					if r.BaseAssumed {
						a.printf("             guessed; confirm with: tspace repo update %s %s --base BASE\n", t.Name, name)
					}
				}
				a.printf("  worktree:  %s\n", r.Worktree)
				a.printf("  agent:     %s\n", agentDisplay(task.Repo{Agent: t.RepoAgent(name)}))
				if resume := t.ResumeFor(name); len(resume) > 0 && name != t.AgentRepo {
					a.printf("  resume:    %s\n", shellJoin(resume))
				}
				if r.PostCreate != "" {
					a.printf("  post_create: %s\n", r.PostCreate)
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print machine-readable JSON instead of text")
	return cmd
}

func (a *App) newTaskRmCmd() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "rm NAME",
		Short: "Delete a task record",
		Long: "rm deletes the task's record. It refuses while worktrees still exist:\n" +
			"tearing down worktrees is `tspace clean`, and conflating the two is how a\n" +
			"tool ends up deleting something you only wanted to tidy.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			t, unlock, err := a.lockTask(cmd, args[0], true)
			if err != nil {
				return err
			}
			defer unlock()

			if !force {
				var present []string
				for _, name := range t.RepoNames() {
					if info, err := os.Stat(t.Repos[name].Worktree); err == nil && info.IsDir() {
						present = append(present, name)
					}
				}
				if len(present) > 0 {
					return fmt.Errorf(
						"task %q still has worktrees for %s\nrun `tspace clean %s` first, or `tspace task rm %s --force` to drop the record and leave them on disk",
						t.Name, joinNames(present), t.Name, t.Name)
				}
			}

			// Notes go with the record, as they always have. With --force the
			// worktrees are still on disk, so the brief describing them stays
			// too — the same reasoning as `repo rm` leaving a worktree behind.
			if force {
				if err := a.deleteRecord(t); err != nil {
					return err
				}
				if note, err := a.Store.ReadNote(t.Name, task.BriefFile); err == nil && note.Text != "" {
					a.warnf("note: the task's notes were left at %s\n", a.Store.ContextDir(t.Name))
				}
				return nil
			}
			return a.deleteTask(t)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false,
		"delete the record even if worktrees still exist on disk")
	return cmd
}

// deleteTask removes a task whose worktrees are gone: its record and its notes.
func (a *App) deleteTask(t *task.Task) error { return a.workspace().Delete(t) }

// deleteRecord removes the task's record alone.
func (a *App) deleteRecord(t *task.Task) error { return a.workspace().DeleteRecord(t) }

// runCmd runs another command as if it had been typed, with this App's output.
func (a *App) runCmd(ctx context.Context, build func(*App) *cobra.Command, args ...string) error {
	c := build(a)
	c.SetArgs(args)
	c.SetOut(a.Out)
	c.SetErr(a.Err)
	c.SilenceUsage, c.SilenceErrors = true, true
	return c.ExecuteContext(ctx)
}

func agentDisplay(r task.Repo) string {
	if len(r.Agent) == 0 {
		return "shell"
	}
	return shellJoin(r.Agent)
}
