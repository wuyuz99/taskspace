package cli

import (
	"fmt"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/wuyuz99/taskspace/internal/task"
)

func (a *App) newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Show and set user-wide defaults",
		Long: "Defaults live in $AW_ROOT/config.json. They are applied when a task is\n" +
			"created and recorded on it, so changing one never changes an existing task.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.Store.LoadConfig()
			if err != nil {
				return err
			}
			a.printf("config: %s\n", a.Store.ConfigPath())
			a.printf("agent:  %s\n", defaultAgentDisplay(c.Agent))
			a.printf("branch: %s (new repositories' default branch)\n", c.Branch("TASK"))
			if len(c.Repos) == 0 {
				a.printf("repos:  none registered (aw config repo NAME PATH)\n")
				return nil
			}
			a.printf("repos:\n")
			a.printRepoCatalog(c)
			return nil
		},
	}
	cmd.AddCommand(a.newConfigAgentCmd(), a.newConfigRepoCmd(), a.newConfigBranchPrefixCmd())
	return cmd
}

func (a *App) newConfigRepoCmd() *cobra.Command {
	var unset bool
	cmd := &cobra.Command{
		Use:   "repo [NAME [PATH] | NAME --unset]",
		Short: "Register source repositories by name",
		Long: "Register each repository you work in once, then give tasks its name\n" +
			"instead of a path:\n\n" +
			"  aw config repo backend ~/src/backend\n" +
			"  aw task create PAY-142 backend frontend@feature/x infra:develop\n\n" +
			"There are no add, list or rm subcommands; the arguments say which:\n\n" +
			"  aw config repo                  list the registered repositories\n" +
			"  aw config repo backend          print backend's path\n" +
			"  aw config repo backend PATH     register backend, or move it to PATH\n" +
			"  aw config repo backend --unset  forget backend\n\n" +
			"Tasks record the path when a repository is added, so moving or\n" +
			"unregistering one later leaves existing tasks alone.",
		Args: cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.Store.LoadConfig()
			if err != nil {
				return err
			}
			switch {
			case unset && len(args) != 1:
				return fmt.Errorf("--unset takes exactly one repository name")
			case len(args) == 0:
				if len(c.Repos) == 0 {
					a.printf("no repositories registered; add one with: aw config repo NAME PATH\n")
					return nil
				}
				a.printRepoCatalog(c)
				return nil
			case len(args) == 1 && !unset:
				p, ok := c.Repos[args[0]]
				if !ok {
					if hint, verb := configRepoVerbs[args[0]]; verb {
						return fmt.Errorf("aw config repo has no %q subcommand; %s", args[0], hint)
					}
					return fmt.Errorf("no repository registered as %q%s", args[0], registeredHint(c))
				}
				a.printf("%s\n", p)
				return nil
			}

			name := args[0]
			// `aw config repo add ~/src/api` reads as a subcommand, and would
			// otherwise quietly register a repository called "add".
			if hint, verb := configRepoVerbs[name]; verb && !unset {
				return fmt.Errorf("%q cannot be a repository name, because it reads as a subcommand; %s", name, hint)
			}
			if unset {
				if _, ok := c.Repos[name]; !ok {
					return fmt.Errorf("no repository registered as %q%s", name, registeredHint(c))
				}
				delete(c.Repos, name)
				if err := a.Store.SaveConfig(c); err != nil {
					return err
				}
				a.printf("unregistered %s; tasks that already use it are unchanged\n", name)
				return nil
			}
			if err := task.ValidateName("repo", name); err != nil {
				return err
			}
			canonical, err := a.Git.Validate(cmd.Context(), expandHome(args[1]))
			if err != nil {
				return err
			}
			for _, other := range c.RepoNames() {
				if other != name && c.Repos[other] == canonical {
					return fmt.Errorf("%s is already registered as %q", canonical, other)
				}
			}
			if c.Repos == nil {
				c.Repos = map[string]string{}
			}
			c.Repos[name] = canonical
			if err := a.Store.SaveConfig(c); err != nil {
				return err
			}
			a.printf("registered %s: %s\n", name, canonical)
			return nil
		},
	}
	cmd.Flags().BoolVar(&unset, "unset", false, "forget a registered repository")
	return cmd
}

func (a *App) newConfigBranchPrefixCmd() *cobra.Command {
	var unset bool
	cmd := &cobra.Command{
		Use:   "branch-prefix [PREFIX | --unset]",
		Short: "Show or set the prefix of new task branches",
		Long: "A repository added to a task without a branch of its own gets\n" +
			"PREFIX + TASK, by default agent/TASK:\n\n" +
			"  aw config branch-prefix me/        # me/PAY-142\n" +
			"  aw config branch-prefix ''         # PAY-142\n\n" +
			"Branches are recorded when a repository is added, so changing the prefix\n" +
			"leaves existing tasks alone.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if unset && len(args) > 0 {
				return fmt.Errorf("choose either a prefix or --unset")
			}
			c, err := a.Store.LoadConfig()
			if err != nil {
				return err
			}
			switch {
			case unset:
				c.BranchPrefix = nil
			case len(args) == 0:
				a.printf("%s (e.g. %s)\n", strconv.Quote(c.Branch("")), c.Branch("PAY-142"))
				return nil
			default:
				prefix := args[0]
				if err := a.Git.CheckBranchName(cmd.Context(), prefix+"PAY-142"); err != nil {
					return fmt.Errorf("prefix %q does not make valid branch names: %w", prefix, err)
				}
				c.BranchPrefix = &prefix
			}
			if err := a.Store.SaveConfig(c); err != nil {
				return err
			}
			a.printf("new task branches: %s (applies to repositories added from now on)\n", c.Branch("TASK"))
			return nil
		},
	}
	cmd.Flags().BoolVar(&unset, "unset", false, "restore the default prefix, agent/")
	return cmd
}

// configRepoVerbs are the subcommands people expect `aw config repo` to have,
// each with what to type instead.
var configRepoVerbs = map[string]string{
	"add":    "register one with: aw config repo NAME PATH",
	"set":    "register one with: aw config repo NAME PATH",
	"list":   "list them with: aw config repo",
	"ls":     "list them with: aw config repo",
	"rm":     "forget one with: aw config repo NAME --unset",
	"remove": "forget one with: aw config repo NAME --unset",
}

func (a *App) printRepoCatalog(c *task.Config) {
	tw := tabwriter.NewWriter(a.Out, 0, 0, 2, ' ', 0)
	for _, name := range c.RepoNames() {
		fmt.Fprintf(tw, "  %s\t%s\n", name, c.Repos[name])
	}
	tw.Flush()
}

func registeredHint(c *task.Config) string {
	if len(c.Repos) == 0 {
		return "; register it with: aw config repo NAME PATH"
	}
	return " (registered: " + joinNames(c.RepoNames()) + ")"
}

func (a *App) newConfigAgentCmd() *cobra.Command {
	var unset bool
	cmd := &cobra.Command{
		Use:   "agent [COMMAND [ARGS...] | --unset]",
		Short: "Show or set the default task-root agent",
		Long: "With a command, new tasks run it in their root window, unless task create\n" +
			"is given --agent or --shell, or its --project recipe names an agent.\n" +
			"Everything after COMMAND is passed to it, flags included:\n\n" +
			"  aw config agent claude --model opus\n\n" +
			"Existing tasks keep the agent they were created with; change one with\n" +
			"aw task update TASK --agent CMD.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if unset && len(args) > 0 {
				return fmt.Errorf("choose either a command or --unset")
			}
			c, err := a.Store.LoadConfig()
			if err != nil {
				return err
			}
			switch {
			case unset:
				c.Agent = nil
			case len(args) == 0:
				a.printf("%s\n", defaultAgentDisplay(c.Agent))
				return nil
			case strings.TrimSpace(args[0]) == "":
				return fmt.Errorf("agent command is empty; use --unset to clear the default")
			default:
				c.Agent = args
			}
			if err := a.Store.SaveConfig(c); err != nil {
				return err
			}
			if unset {
				a.printf("cleared the default agent; new tasks' root windows run a shell\n")
				return nil
			}
			a.printf("default agent: %s (applies to tasks created from now on)\n", shellJoin(c.Agent))
			return nil
		},
	}
	// Flags after COMMAND belong to the agent, not to aw.
	cmd.Flags().SetInterspersed(false)
	cmd.Flags().BoolVar(&unset, "unset", false, "clear the default, so new tasks' root windows run a shell")
	return cmd
}

func defaultAgentDisplay(argv []string) string {
	if len(argv) == 0 {
		return "none (task roots run a shell)"
	}
	return shellJoin(argv)
}
