package cli

import (
	"sort"
	"strings"

	"github.com/wuyuz99/taskspace/internal/status"
	"github.com/wuyuz99/taskspace/internal/task"
)

// signalUrgency and runtimeUrgency rank states by how much they need a
// person, so a collapsed task shows the worst of its windows.
func signalUrgency(s *task.Signal) int {
	if s == nil {
		return 0
	}
	switch s.State {
	case task.SignalFailed:
		return 5
	case task.SignalBlocked:
		return 4
	case task.SignalWorking:
		return 3
	case task.SignalDone:
		return 2
	default:
		return 1
	}
}

func runtimeUrgency(s status.RuntimeState) int {
	switch s {
	case status.RuntimeUnknown:
		return 5
	case status.RuntimeDead:
		return 4
	case status.RuntimeNoWindow:
		return 3
	case status.RuntimeRunning:
		return 2
	default:
		return 1
	}
}

// Quiet tasks take one row until explicitly expanded. Tasks needing attention
// stay expanded so an individual failure cannot be hidden by a summary.
// Extra windows count as rows but not toward the task's state: a closed
// saved window, or a finished test run left dead, is not the task failing.
func collapseDashRows(rows []dashRow, expanded map[string]bool) []dashRow {
	var result []dashRow
	for first := 0; first < len(rows); {
		end := first + 1
		for end < len(rows) && rows[end].Task == rows[first].Task {
			end++
		}
		group := rows[first:end]
		open := expanded[group[0].Task]
		for _, r := range group {
			open = open || (!r.extra() && r.Runtime == status.RuntimeDead) || signalUrgency(r.Signal) >= 4
		}
		if open || len(group) == 1 {
			result = append(result, group...)
			first = end
			continue
		}
		summary := group[0]
		for _, r := range group {
			if r.root() {
				summary = r
				break
			}
		}
		summary.Summary = len(group)
		git := map[string]bool{}
		for _, r := range group {
			if !r.extra() && runtimeUrgency(r.Runtime) > runtimeUrgency(summary.Runtime) {
				summary.Runtime = r.Runtime
			}
			if signalUrgency(r.Signal) > signalUrgency(summary.Signal) {
				summary.Signal = r.Signal
			}
			if r.Git != "" && r.Git != "-" {
				git[r.Git] = true
			}
		}
		var states []string
		for s := range git {
			states = append(states, s)
		}
		sort.Strings(states)
		summary.Git = strings.Join(states, "; ")
		result = append(result, summary)
		first = end
	}
	return result
}
