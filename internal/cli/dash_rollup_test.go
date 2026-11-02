package cli

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/wuyuz99/taskspace/internal/status"
	"github.com/wuyuz99/taskspace/internal/task"
)

func TestRollupText(t *testing.T) {
	for _, c := range []struct {
		in   status.Rollup
		want string
	}{
		{status.Rollup{}, "-"},
		{status.Rollup{Repos: 1}, "1 repo, clean"},
		{status.Rollup{Repos: 3, Holding: []string{"a"}, Unpushed: []string{"a", "b"}}, "3 repos, 1 dirty, 2 unpushed"},
		{status.Rollup{Repos: 2, Missing: 2}, "2 repos, 2 missing"},
		{status.Rollup{Repos: 2, Errors: 1}, "2 repos, 1 error"},
	} {
		if got := rollupText(c.in); got != c.want {
			t.Errorf("rollupText(%+v) = %q, want %q", c.in, got, c.want)
		}
	}
}

// A collapsed task's row says where the task stands, and the detail pane
// names the repositories.
func TestDashShowsATasksRollup(t *testing.T) {
	m := newDashModel(t.Context(), nil, time.Second)
	m = drive(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
	m = drive(t, m, dashDataMsg(dashData{
		Rows: []dashRow{
			{Task: "A", Window: task.RootWindowName, Runtime: status.RuntimeRunning},
			{Task: "A", Window: "api", Runtime: status.RuntimeRunning},
			{Task: "A", Window: "web", Runtime: status.RuntimeRunning},
		},
		Tasks: map[string]dashTask{"A": {Rollup: status.Rollup{Repos: 2, Holding: []string{"api"}, Unpushed: []string{"web"}}}},
	}))
	view := m.View()
	for _, want := range []string{"2 repos, 1 dirty, 1 unpushed", "clean would refuse: uncommitted work or stashes in api", "only on this machine: web"} {
		if !strings.Contains(view, want) {
			t.Errorf("missing %q:\n%s", want, view)
		}
	}
}
