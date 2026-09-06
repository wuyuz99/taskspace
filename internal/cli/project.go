package cli

import (
	"errors"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/aw-tool/aw/internal/task"
	"github.com/spf13/cobra"
)

func (a *App) newProjectCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "project", Short: "Save and inspect reusable workspace recipes"}
	var from string
	var replace bool
	save := &cobra.Command{Use: "save NAME --from TASK", Short: "Save a task's reusable repository configuration",
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
			if err != nil {
				return err
			}
			if len(projects) == 0 {
				a.printf("no projects yet; save one with: aw project save NAME --from TASK\n")
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
