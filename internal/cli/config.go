package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
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
			return nil
		},
	}
	cmd.AddCommand(a.newConfigAgentCmd())
	return cmd
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
