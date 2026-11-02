package cli

import (
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"github.com/wuyuz99/taskspace/internal/agents"
	tsexec "github.com/wuyuz99/taskspace/internal/exec"
	"github.com/wuyuz99/taskspace/internal/status"
	"github.com/wuyuz99/taskspace/internal/task"
)

// agentStatusSources are the agents whose own published status tspace can
// read, each an opt-in integration named after its agent. Core status only
// knows status.PublishedReader.
var agentStatusSources = map[string]func(tsexec.Runner) status.PublishedReader{
	"claude": func(r tsexec.Runner) status.PublishedReader { return agents.ClaudeAgents{Run: r} },
}

// agentStatusDeprecated says why config agent-status is going (#140).
const agentStatusDeprecated = "it is deprecated and goes in the release after 0.4: " +
	"Claude Code's hooks report the same states; install them with: tspace config hooks claude"

// agentStatusReaders are the integrations the config turns on.
func agentStatusReaders(c *task.Config, r tsexec.Runner) []status.PublishedReader {
	var out []status.PublishedReader
	for _, name := range c.AgentStatus {
		if mk, ok := agentStatusSources[name]; ok {
			out = append(out, mk(r))
		}
	}
	return out
}

func (a *App) newConfigAgentStatusCmd() *cobra.Command {
	var unset bool
	names := make([]string, 0, len(agentStatusSources))
	for n := range agentStatusSources {
		names = append(names, n)
	}
	slices.Sort(names)
	cmd := &cobra.Command{
		Use:        "agent-status [AGENT... | --unset]",
		Short:      "Show or choose the agents whose own status tspace reads",
		Deprecated: agentStatusDeprecated,
		Long: "Some agents publish what their sessions are doing through an interface of\n" +
			"their own. Naming one here has status and dash read it, matching each\n" +
			"session to the window running in its directory. It is off unless chosen,\n" +
			"and costs one call to the agent's CLI per refresh. Known: " + strings.Join(names, ", ") + ".\n\n" +
			"  claude  `claude agents --json`: busy, waiting (blocked, with what it\n" +
			"          waits for) or idle, for each interactive Claude Code session\n\n" +
			"A published status comes after the agent's own `tspace signal`, but\n" +
			"outranks a working signal it contradicts, and the bell; see\n" +
			"`tspace status --help`. It needs no hooks, so it also covers an agent\n" +
			"interrupted before its Stop hook could run.\n\n" +
			"  tspace config agent-status claude\n" +
			"  tspace config agent-status --unset",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if unset && len(args) > 0 {
				return fmt.Errorf("choose either agents or --unset")
			}
			for _, n := range args {
				if _, ok := agentStatusSources[n]; !ok {
					return fmt.Errorf("tspace cannot read %q's status (can: %s)", n, strings.Join(names, ", "))
				}
			}
			c, unlock, err := a.configFor(cmd, unset || len(args) > 0)
			if err != nil {
				return err
			}
			defer unlock()
			switch {
			case unset:
				c.AgentStatus = nil
			case len(args) == 0:
				a.printf("%s\n", agentStatusDisplay(c.AgentStatus))
				return nil
			default:
				c.AgentStatus = slices.Compact(slices.Sorted(slices.Values(args)))
			}
			if err := a.Store.SaveConfig(c); err != nil {
				return err
			}
			a.printf("agent status read from: %s\n", agentStatusDisplay(c.AgentStatus))
			return nil
		},
	}
	cmd.Flags().BoolVar(&unset, "unset", false, "read no agent's own status")
	return cmd
}

func agentStatusDisplay(names []string) string {
	if len(names) == 0 {
		return "none; status comes from signals and the multiplexer"
	}
	return strings.Join(names, ", ")
}
