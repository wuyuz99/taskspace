package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	tsexec "github.com/wuyuz99/taskspace/internal/exec"
	"github.com/wuyuz99/taskspace/internal/fspath"
	"github.com/wuyuz99/taskspace/internal/status"
	"github.com/wuyuz99/taskspace/internal/task"
)

// statusTimeout bounds one integration's answer, so a hung agent CLI
// costs a refresh a few seconds rather than the dashboard.
const statusTimeout = 5 * time.Second

// ClaudeAgents reads `claude agents --json`, which lists Claude Code's
// running sessions, interactive ones included, with what each is doing.
// Claude Code documents it for scripts to poll.
type ClaudeAgents struct{ Run tsexec.Runner }

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
func (c ClaudeAgents) Published(ctx context.Context) (map[string]status.Published, error) {
	ctx, cancel := context.WithTimeout(ctx, statusTimeout)
	defer cancel()
	res, err := c.Run.Run(ctx, "", "claude", "agents", "--json")
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
		key := fspath.Key(s.Cwd)
		if prev, ok := out[key]; ok && rank[prev.State] >= rank[state] {
			continue
		}
		out[key] = status.Published{State: state, Message: s.WaitingFor, Source: "claude"}
	}
	return out, nil
}
