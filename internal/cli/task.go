package cli

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/wuyuz99/taskspace/internal/task"
)

func (a *App) newTaskCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "task",
		Short: "Create, list, inspect and remove tasks",
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

	cmd := &cobra.Command{
		Use:   "create NAME [REPO...]",
		Short: "Create a task",
		Long: "create records a task and the repositories it spans. Each REPO is a name\n" +
			"registered with `aw config repo` or a path, optionally with a branch and\n" +
			"a base, as for `aw repo add`:\n\n" +
			"  aw task create PAY-142 backend frontend@feature/x infra:develop\n\n" +
			"No worktree is created until `aw start` or `aw open`.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			brief, err := details.readBrief()
			if err != nil {
				return err
			}
			if err := agent.validate(cmd); err != nil {
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
			if cmd.Flags().Changed("project") {
				p, err := a.Store.LoadProject(project)
				if err != nil {
					return err
				}
				t.Agent = append([]string(nil), p.Agent...)
				if len(p.Agent) > 0 {
					agentFrom = "project " + p.Name
				}
				for _, repoName := range p.RepoNames() {
					r := p.Repos[repoName]
					c, err := config()
					if err != nil {
						return err
					}
					if err := a.addRepo(cmd.Context(), t, c, r.Path, addRepoOptions{Name: repoName, Base: r.Base, Agent: r.Agent, PostCreate: r.PostCreate}); err != nil {
						return err
					}
				}
			}

			// Each REPO is sugar for a following `aw repo add`.
			for _, spec := range append(repos, args[1:]...) {
				c, err := config()
				if err != nil {
					return err
				}
				if err := a.addRepoSpec(cmd.Context(), t, c, spec, addRepoOptions{}); err != nil {
					return err
				}
			}
			switch {
			case agent.apply(cmd, t):
				agentFrom = ""
			case len(t.Agent) == 0:
				cfg, err := config()
				if err != nil {
					return err
				}
				if len(cfg.Agent) > 0 {
					t.Agent = append([]string(nil), cfg.Agent...)
					agentFrom = "aw config"
				}
			}
			if err := a.Store.CreatePrepared(t); err != nil {
				return err
			}
			// The brief is written after the record exists, so a failure here
			// leaves a task without a brief rather than a note without a task.
			if err := a.applyBrief(cmd, t, brief); err != nil {
				return err
			}
			a.printf("created task %s\n", name)
			if len(t.Agent) > 0 {
				from := ""
				if agentFrom != "" {
					from = ", from " + agentFrom
				}
				a.printf("  agent: %s (task root%s)\n", shellJoin(t.Agent), from)
			}
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&repos, "repo", nil,
		"attach a repository, the same as a REPO argument (repeatable)")
	cmd.Flags().StringVar(&project, "project", "", "snapshot a saved project recipe into this task")
	details.flags(cmd)
	agent.flags(cmd)
	return cmd
}

func (a *App) newTaskListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List tasks",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			tasks, err := a.Store.List()
			if err := a.listed(err); err != nil {
				return err
			}
			if len(tasks) == 0 {
				a.printf("no tasks yet; create one with: aw task create NAME\n")
				return nil
			}

			tw := tabwriter.NewWriter(a.Out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "WORKSPACE\tTICKET\tTITLE\tLOCAL SUMMARY\tUPDATED (UTC)\tREPOS")
			for _, t := range tasks {
				_, handoff, err := a.notes(t)
				if err != nil {
					a.warnf("aw: %s: %v\n", t.Name, err)
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", t.Name, dash(t.Ticket), dash(oneLine(t.Title)),
					dash(noteSummary(handoff.Text)), handoffTime(handoff), dash(joinNames(t.RepoNames())))
			}
			return tw.Flush()
		},
	}
}

func (a *App) newTaskShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show NAME",
		Short: "Show a task's repositories in detail",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			t, err := a.loadTask(args[0])
			if err != nil {
				return err
			}

			a.printf("task:     %s\n", t.Name)
			a.printf("session:  %s\n", a.Runtime.SessionName(t.Name))
			a.printf("workspace: %s\n", a.Store.TaskDir(t.Name))
			a.printf("context:   %s\n", a.Store.ContextDir(t.Name))
			a.printf("agent:     %s (window %s, at the workspace root)\n", agentDisplay(task.Repo{Agent: t.Agent}), task.RootWindowName)
			a.printTaskContext(t)
			if len(t.Repos) == 0 {
				a.printf("\nno repositories yet; add one with:\n  aw repo add %s REPO\n", t.Name)
				return nil
			}

			for _, name := range t.RepoNames() {
				r := t.Repos[name]
				a.printf("\n%s\n", name)
				a.printf("  repo:      %s\n", r.Path)
				a.printf("  branch:    %s\n", r.Branch)
				if r.Base != "" {
					a.printf("  base:      %s\n", r.Base)
				}
				a.printf("  worktree:  %s\n", r.Worktree)
				a.printf("  agent:     %s\n", agentDisplay(r))
				if r.PostCreate != "" {
					a.printf("  post_create: %s\n", r.PostCreate)
				}
			}
			return nil
		},
	}
}

func (a *App) newTaskRmCmd() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "rm NAME",
		Short: "Delete a task record",
		Long: "rm deletes the task's record. It refuses while worktrees still exist:\n" +
			"tearing down worktrees is `aw clean`, and conflating the two is how a\n" +
			"tool ends up deleting something you only wanted to tidy.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			t, err := a.loadTask(args[0])
			if err != nil {
				return err
			}

			if !force {
				var present []string
				for _, name := range t.RepoNames() {
					if info, err := os.Stat(t.Repos[name].Worktree); err == nil && info.IsDir() {
						present = append(present, name)
					}
				}
				if len(present) > 0 {
					return fmt.Errorf(
						"task %q still has worktrees for %s\nrun `aw clean %s` first, or `aw task rm %s --force` to drop the record and leave them on disk",
						t.Name, joinNames(present), t.Name, t.Name)
				}
			}

			if err := a.Store.Delete(t.Name); err != nil {
				return err
			}
			a.printf("removed task %s\n", t.Name)

			// Notes go with the record, as they always have. With --force the
			// worktrees are still on disk, so the brief describing them stays
			// too — the same reasoning as `repo rm` leaving a worktree behind.
			if force {
				if note, err := a.Store.ReadNote(t.Name, task.BriefFile); err == nil && note.Text != "" {
					a.warnf("note: the task's notes were left at %s\n", a.Store.ContextDir(t.Name))
				}
				return nil
			}
			held, err := a.Store.RemoveNotes(t.Name)
			if err != nil {
				return err
			}
			if len(held) > 0 {
				a.printf("deleted its notes (%s)\n", joinNames(held))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false,
		"delete the record even if worktrees still exist on disk")
	return cmd
}

func agentDisplay(r task.Repo) string {
	if len(r.Agent) == 0 {
		return "shell"
	}
	return shellJoin(r.Agent)
}
