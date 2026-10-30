package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"
	tsexec "github.com/wuyuz99/taskspace/internal/exec"
	"github.com/wuyuz99/taskspace/internal/status"
	"github.com/wuyuz99/taskspace/internal/task"
)

// agentStatusSources are the agents whose own published status tspace can
// read, each an opt-in integration named after its agent. Core status only
// knows status.PublishedReader.
var agentStatusSources = map[string]func(tsexec.Runner) status.PublishedReader{
	"claude": func(r tsexec.Runner) status.PublishedReader { return claudeAgents{run: r} },
}

// agentStatusTimeout bounds one integration's answer, so a hung agent CLI
// costs a refresh a few seconds rather than the dashboard.
const agentStatusTimeout = 5 * time.Second

// claudeAgents reads `claude agents --json`, which lists Claude Code's
// running sessions, interactive ones included, with what each is doing.
// Claude Code documents it for scripts to poll.
type claudeAgents struct{ run tsexec.Runner }

// claudeSession is the part of one `claude agents --json` entry tspace reads.
type claudeSession struct {
	Cwd        string `json:"cwd"`
	Kind       string `json:"kind"`
	Status     string `json:"status"`
	WaitingFor string `json:"waitingFor"`
}

// Published implements status.PublishedReader. Only interactive sessions
// are read: a background session is not in a tspace window, whatever its
// directory. Of several in one directory, the one most in need of a person
// speaks for it.
func (c claudeAgents) Published(ctx context.Context) (map[string]status.Published, error) {
	ctx, cancel := context.WithTimeout(ctx, agentStatusTimeout)
	defer cancel()
	res, err := c.run.Run(ctx, "", "claude", "agents", "--json")
	if err != nil {
		return nil, err
	}
	var sessions []claudeSession
	if err := json.Unmarshal([]byte(res.Stdout), &sessions); err != nil {
		return nil, fmt.Errorf("claude agents --json: %w", err)
	}
	rank := map[status.ReportState]int{
		status.ReportState(task.SignalBlocked): 3,
		status.ReportState(task.SignalWorking): 2,
		status.ReportIdle:                      1,
	}
	out := map[string]status.Published{}
	for _, s := range sessions {
		var state status.ReportState
		switch s.Status {
		case "busy":
			state = status.ReportState(task.SignalWorking)
		case "waiting":
			state = status.ReportState(task.SignalBlocked)
		case "idle":
			state = status.ReportIdle
		default:
			continue
		}
		if s.Kind != "interactive" || s.Cwd == "" {
			continue
		}
		key := status.DirKey(s.Cwd)
		if prev, ok := out[key]; ok && rank[prev.State] >= rank[state] {
			continue
		}
		out[key] = status.Published{State: state, Message: s.WaitingFor, Source: "claude"}
	}
	return out, nil
}

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
		Use:   "agent-status [AGENT... | --unset]",
		Short: "Show or choose the agents whose own status tspace reads",
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
			c, err := a.Store.LoadConfig()
			if err != nil {
				return err
			}
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
