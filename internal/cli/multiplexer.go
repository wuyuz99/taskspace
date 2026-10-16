package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	tsexec "github.com/wuyuz99/taskspace/internal/exec"
	"github.com/wuyuz99/taskspace/internal/runtime"
	"github.com/wuyuz99/taskspace/internal/task"
)

func (a *App) newConfigMultiplexerCmd() *cobra.Command {
	var unset bool
	cmd := &cobra.Command{
		Use:   "multiplexer [NAME [COMMAND [ARGS...]] | --unset]",
		Short: "Show or choose the terminal multiplexer sessions run in",
		Long: "Sessions run in tmux unless another multiplexer is chosen. zellij (0.44 or\n" +
			"later) is also built in:\n\n" +
			"  tspace config multiplexer zellij\n\n" +
			"Any other multiplexer works through an adapter: a program speaking\n" +
			"tspace's adapter protocol (see docs/MULTIPLEXERS.md). A NAME that is not\n" +
			"built in runs tspace-mux-NAME from PATH, or the COMMAND given here:\n\n" +
			"  tspace config multiplexer wezterm ~/bin/tspace-wezterm --flag\n\n" +
			"A COMMAND given for a built-in name replaces the built-in support;\n" +
			"choosing the name again without one, or --unset for tmux, goes back to it.\n" +
			"$TSPACE_MULTIPLEXER overrides the choice for one shell or command.\n\n" +
			"The choice applies to every task at once: tspace looks for each task's\n" +
			"session in the chosen multiplexer, so stop running sessions before switching.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if unset && len(args) > 0 {
				return fmt.Errorf("choose either a multiplexer or --unset")
			}
			c, err := a.Store.LoadConfig()
			if err != nil {
				return err
			}
			if !unset && len(args) == 0 {
				a.printMultiplexer(c)
				return nil
			}

			prev := multiplexerName(c)
			name := runtime.Default
			if !unset {
				name = args[0]
				if err := runtime.ValidateName(name); err != nil {
					return err
				}
			}
			c.Multiplexer = name
			// The command given, or its absence, is what NAME runs from now on.
			if len(args) > 1 {
				if c.Adapters == nil {
					c.Adapters = map[string][]string{}
				}
				c.Adapters[name] = append([]string{expandHome(args[1])}, args[2:]...)
			} else {
				delete(c.Adapters, name)
				if len(c.Adapters) == 0 {
					c.Adapters = nil
				}
			}
			if name == runtime.Default {
				// The default is implied, so the config stays empty for it.
				c.Multiplexer = ""
			}
			if err := a.Store.SaveConfig(c); err != nil {
				return err
			}

			a.printf("multiplexer: %s\n", multiplexerDisplay(name, c))
			if env := os.Getenv("TSPACE_MULTIPLEXER"); env != "" && env != name {
				a.warnf("note: $TSPACE_MULTIPLEXER=%s overrides this in the current shell\n", env)
			}
			if prev != name {
				a.warnRunningSessions(cmd, name)
			}
			rt, err := runtime.New(name, c.Adapters[name], tsexec.Real())
			if err == nil {
				err = rt.Available(cmd.Context())
			}
			if err != nil {
				a.warnf("warning: %v\n", err)
			}
			return nil
		},
	}
	// Flags after COMMAND belong to the adapter, not to tspace.
	cmd.Flags().SetInterspersed(false)
	cmd.Flags().BoolVar(&unset, "unset", false, "go back to the default, "+runtime.Default)
	return cmd
}

func (a *App) printMultiplexer(c *task.Config) {
	name := multiplexerName(c)
	source := "default"
	switch {
	case os.Getenv("TSPACE_MULTIPLEXER") != "":
		source = "from $TSPACE_MULTIPLEXER"
	case c.Multiplexer != "":
		source = "from " + a.Store.ConfigPath()
	}
	a.printf("%s (%s)\n", multiplexerDisplay(name, c), source)
	a.printf("built in: %s; others need an adapter (tspace config multiplexer --help)\n",
		strings.Join(runtime.Builtins(), ", "))
}

// multiplexerDisplay names a multiplexer and says how tspace drives it.
func multiplexerDisplay(name string, c *task.Config) string {
	if command := c.Adapters[name]; len(command) > 0 {
		return name + ", through adapter " + shellJoin(command)
	}
	if runtime.IsBuiltin(name) {
		return name + " (built in)"
	}
	return name + ", through adapter " + runtime.AdapterPrefix + name + " on PATH"
}

// warnRunningSessions names the tasks whose sessions run in the multiplexer
// being switched away from: tspace will no longer see them.
func (a *App) warnRunningSessions(cmd *cobra.Command, next string) {
	if a.Runtime == nil || a.Runtime.Available(cmd.Context()) != nil {
		return
	}
	tasks, _ := a.Store.List()
	var running []string
	for _, t := range tasks {
		if ok, err := a.Runtime.HasSession(cmd.Context(), a.Runtime.SessionName(t.Name)); err == nil && ok {
			running = append(running, t.Name)
		}
	}
	if len(running) == 0 {
		return
	}
	a.warnf("note: sessions still running in the previous multiplexer are not visible from %s: %s\n"+
		"      to move one, switch back, run tspace stop TASK, then switch again\n", next, joinNames(running))
}
