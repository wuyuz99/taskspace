package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/wuyuz99/taskspace/internal/agents"
)

func (a *App) newConfigHooksCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hooks",
		Short: "Install the hooks that let agents report their state",
		Long: "hooks wires an agent's own hooks to `tspace signal`, so that status and\n" +
			"dash show which agents are working, blocked or done. Each subcommand\n" +
			"knows one agent's settings file. Nothing else in tspace depends on it:\n" +
			"any agent with hooks of its own can run `tspace signal --quiet STATE`,\n" +
			"and one without can write .context/signals/WINDOW.json itself; see\n" +
			"`tspace signal --help`, and docs/STATUS.md for other agents' recipes.",
	}
	cmd.AddCommand(a.newConfigHooksAgentCmd(agents.Claude), a.newConfigHooksAgentCmd(agents.Gemini))
	return cmd
}

func (a *App) newConfigHooksAgentCmd(agent agents.HookAgent) *cobra.Command {
	var settings string
	var remove bool
	var table strings.Builder
	var help strings.Builder
	for _, h := range agent.Hooks {
		fmt.Fprintf(&table, "  %-18s %s\n", h.Event, h.Command)
		if h.Note != "" {
			fmt.Fprintf(&help, "  %-18s %s  (%s)\n", h.Event, h.Command, h.Note)
		} else {
			fmt.Fprintf(&help, "  %-18s %s\n", h.Event, h.Command)
		}
	}
	long := fmt.Sprintf("%s adds these hooks to %s's user settings,\n%s:\n\n", agent.Name, agent.Product, agent.SettingsHelp) +
		help.String() + "\n" +
		"They are user-wide rather than in each worktree, where the settings file\n" +
		"would be an untracked file that clean refuses to remove. So they run in\n" +
		"every " + agent.Product + " session; --quiet makes them do nothing outside a task.\n\n" +
		"Everything else in the file is kept as it is, other hooks included. A\n" +
		"hook that is only a `tspace signal` call is replaced, so running this\n" +
		"again changes nothing. --remove takes the tspace hooks out again. A\n" +
		"settings file that is a symbolic link is written through the link."
	if agent.Notes != "" {
		long += "\n\n" + agent.Notes
	}
	cmd := &cobra.Command{
		Use:   agent.Name,
		Short: "Install the signal hooks into " + agent.Product + "'s user settings",
		Long:  long,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path := settings
			if path == "" {
				p, err := agent.Settings()
				if err != nil {
					return err
				}
				path = p
			}
			changed, err := agents.UpdateSignalHooks(path, agent.Hooks, !remove)
			if err != nil {
				return err
			}
			switch {
			case !changed && remove:
				a.printf("no tspace hooks in %s\n", path)
			case !changed:
				a.printf("the tspace hooks are already in %s\n", path)
			case remove:
				a.printf("removed the tspace hooks from %s\n", path)
			default:
				a.printf("installed the tspace hooks in %s:\n", path)
				a.printf("%s", table.String())
				a.printf("sessions started from now on use them\n")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&settings, "settings", "", "settings file to change (default: "+agent.Product+"'s user settings)")
	cmd.Flags().BoolVar(&remove, "remove", false, "remove the tspace hooks instead")
	return cmd
}
