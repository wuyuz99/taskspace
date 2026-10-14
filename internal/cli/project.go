package cli

import (
	"errors"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/wuyuz99/taskspace/internal/task"
)

func (a *App) newProjectCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "project", Short: "Save and inspect reusable workspace recipes",
		Long: "A project is a recipe for tasks that span the same repositories: the\n" +
			"task-root agent, and for each repository its path, base, agent and setup\n" +
			"hook. It holds no task name, branch, ticket or notes.\n\n" +
			"  tspace project save shop --from PAY-142     keep PAY-142's setup as a recipe\n" +
			"  tspace task create PAY-143 --project shop   a new task with the same setup\n\n" +
			"A recipe is copied into a task when the task is created, so changing or\n" +
			"replacing one never changes an existing task."}
	var from string
	var replace bool
	save := &cobra.Command{Use: "save NAME --from TASK", Short: "Save a task's reusable repository configuration",
		Long: "save records the task's root agent and, for each of its repositories, the\n" +
			"path, base, agent and setup hook, as the recipe NAME. Branches are not\n" +
			"saved: a task created from the recipe gets its own.",
		Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			if from == "" {
				return fmt.Errorf("specify a source task with --from TASK")
			}
			t, err := a.loadTask(from)
			if err != nil {
				return err
			}
			if err := a.Store.SaveProject(task.ProjectFromTask(args[0], t), replace); err != nil {
				if errors.Is(err, os.ErrExist) {
					return fmt.Errorf("project %q already exists; use --replace to update its recipe", args[0])
				}
				return err
			}
			a.printf("saved project %s from %s; existing tasks are unchanged\n", args[0], from)
			return nil
		}}
	save.Flags().StringVar(&from, "from", "", "task whose repositories form the recipe")
	save.Flags().BoolVar(&replace, "replace", false, "replace an existing recipe; existing tasks remain unchanged")
	list := &cobra.Command{Use: "list", Aliases: []string{"ls"}, Short: "List project recipes", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			projects, err := a.Store.ListProjects()
			if err := a.listed(err); err != nil {
				return err
			}
			if len(projects) == 0 {
				a.printf("no projects yet; save one with: tspace project save NAME --from TASK\n")
				return nil
			}
			tw := tabwriter.NewWriter(a.Out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "PROJECT\tREPOSITORIES")
			for _, p := range projects {
				fmt.Fprintf(tw, "%s\t%s\n", p.Name, joinNames(p.RepoNames()))
			}
			return tw.Flush()
		}}
	show := &cobra.Command{Use: "show NAME", Short: "Inspect a recipe, including commands and setup hooks", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := a.Store.LoadProject(args[0])
			if err != nil {
				return err
			}
			a.printf("project: %s\n", p.Name)
			a.printf("agent:   %s (task root)\n", agentDisplay(task.Repo{Agent: p.Agent}))
			for _, name := range p.RepoNames() {
				r := p.Repos[name]
				base := r.Base
				if base == "" {
					base = "auto (resolved when creating a task)"
				}
				a.printf("\n%s\n  repo: %s\n  base: %s\n  agent: %s\n", name, r.Path, base, agentDisplay(task.Repo{Agent: r.Agent}))
				if r.PostCreate != "" {
					a.printf("  post_create: %s\n", r.PostCreate)
				}
			}
			return nil
		}}
	cmd.AddCommand(save, list, show)
	return cmd
}
