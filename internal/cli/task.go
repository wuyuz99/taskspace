package cli

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/aw-tool/aw/internal/task"
	"github.com/spf13/cobra"
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

	cmd := &cobra.Command{
		Use:   "create NAME",
		Short: "Create a task",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			brief, err := details.readBrief()
			if err != nil {
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
			details.apply(cmd, t, brief)
			if cmd.Flags().Changed("project") {
				p, err := a.Store.LoadProject(project)
				if err != nil {
					return err
				}
				for _, repoName := range p.RepoNames() {
					r := p.Repos[repoName]
					if err := a.addRepo(cmd.Context(), t, repoName, r.Path, addRepoOptions{Base: r.Base, Agent: r.Agent, PostCreate: r.PostCreate}); err != nil {
						return err
					}
				}
			}

			// --repo name=path is sugar for a following `aw repo add`.
			for _, spec := range repos {
				repoName, path, err := parseRepoSpec(spec)
				if err != nil {
					return err
				}
				if err := a.addRepo(cmd.Context(), t, repoName, path, addRepoOptions{}); err != nil {
					return err
				}
			}
			if err := a.Store.CreatePrepared(t); err != nil {
				return err
			}
			a.printf("created task %s\n", name)
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&repos, "repo", nil,
		"attach a repository as name=path (repeatable)")
	cmd.Flags().StringVar(&project, "project", "", "snapshot a saved project recipe into this task")
	details.flags(cmd)
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
				names := t.RepoNames()
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", t.Name, dash(t.Ticket), dash(oneLine(t.Title)), dash(noteSummary(t.Handoff)), handoffTime(t), dash(joinNames(names)))
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
			a.printTaskContext(t)
			if len(t.Repos) == 0 {
				a.printf("\nno repositories yet; add one with:\n  aw repo add %s PATH\n", t.Name)
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
