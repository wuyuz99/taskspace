package cli

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// dashKey is one of dash's keys. key() does what it says;
// TestDashKeysAreHandled keeps the two together.
type dashKey struct {
	// keys is how the help names it, and press is a key to test it with.
	keys, press string
	// help is its line in `tspace dash --help`.
	help string
	// group and label place it in the ? panel; common keeps it in the legend.
	group, label string
	common       bool
}

// dashGroups are the ? panel's groups, in order.
var dashGroups = []string{"Task", "Window", "Repo", "View"}

// dashKeys are listed in the order of the help and the legend.
var dashKeys = []dashKey{
	{keys: "up/down, j/k", press: "j", group: "View", label: "j/k move", help: "select a row (mouse wheel moves one row)"},
	{keys: "enter", press: "enter", group: "Window", label: "enter attach", common: true, help: "attach to that window; detaching returns here"},
	{keys: "space", press: " ", group: "View", label: "space expand", common: true, help: "expand/collapse a task; one that needs you stays expanded"},
	{keys: "s", press: "s", group: "Task", label: "s start", help: "start the task (tspace start)"},
	{keys: "x", press: "x", group: "Task", label: "x stop", help: "stop the task, after confirming (tspace stop)"},
	{keys: "S / X", press: "S", group: "Window", label: "S/X start/stop", help: "start / stop only the selected window (tspace start/stop TASK REPO)"},
	{keys: "r", press: "r", group: "Window", label: "r restart", help: "restart a dead window (tspace restart)"},
	{keys: "D", press: "D", group: "Task", label: "D clean", help: "clean the task, after confirming (tspace clean)"},
	{keys: "R", press: "R", group: "Task", label: "R delete", help: "delete the task, after confirming (tspace clean --rm)"},
	{keys: "-", press: "-", group: "Repo", label: "- remove", help: "remove the selected repository from the task, after\nconfirming (tspace repo rm)"},
	{keys: "m", press: "m", group: "Repo", label: "m move", help: "move the selected repository to another task, in a form\n(tspace repo move)"},
	{keys: "t", press: "t", group: "Window", label: "t tab", help: "open the task in a new terminal tab (tspace config terminal)"},
	{keys: "c", press: "c", group: "Window", label: "c clear", help: "clear the row's signal"},
	{keys: "n", press: "n", group: "Task", label: "n new", common: true, help: "create a task, in a form that takes the screen (tspace task create)"},
	{keys: "N", press: "N", group: "Task", label: "N rename", help: "rename the selected task (tspace task rename)"},
	{keys: "a", press: "a", group: "Repo", label: "a register", help: "register a repository on this machine, in a form (tspace config repo)"},
	{keys: "w", press: "w", group: "Window", label: "w new", help: "open another window at the row's place, in a form: a shell,\nthe task's agent or a command (tspace window -d)"},
	{keys: "W", press: "W", group: "Window", label: "W save", help: "save the selected open extra window (tspace window --save)"},
	{keys: "I", press: "I", group: "Task", label: "I interrupted", help: "start every interrupted task (tspace start --interrupted);\ndash offers it on launch when there are any"},
	{keys: "?", press: "?", group: "View", label: "? keys", common: true, help: "show every key, by what it acts on; any key closes it"},
	{keys: "q", press: "q", group: "View", label: "q quit", common: true, help: "quit"},
}

// dashKeyHelp is the key table in `tspace dash --help`.
func dashKeyHelp() string {
	const column = 14
	lines := make([]string, len(dashKeys))
	for i, k := range dashKeys {
		help := strings.ReplaceAll(k.help, "\n", "\n"+strings.Repeat(" ", 2+column))
		lines[i] = fmt.Sprintf("  %-*s%s", column, k.keys, help)
	}
	return strings.Join(lines, "\n")
}

// dashLegend is the common keys' entries, wrapped to width columns on a
// terminal too narrow for one line.
func dashLegend(width int) []string {
	var labels []string
	for _, k := range dashKeys {
		if k.common {
			labels = append(labels, k.label)
		}
	}
	return wrapLabels(labels, width)
}

// dashKeyPanel is every key by group, each group's name in a column of its
// own, wrapped to width columns.
func dashKeyPanel(width int) []string {
	const column = 8
	var out []string
	for _, group := range dashGroups {
		var labels []string
		for _, k := range dashKeys {
			if k.group == group {
				labels = append(labels, k.label)
			}
		}
		for i, line := range wrapLabels(labels, width-column) {
			name := ""
			if i == 0 {
				name = group
			}
			out = append(out, fmt.Sprintf("%-*s%s", column, name, line))
		}
	}
	return out
}

func wrapLabels(labels []string, width int) []string {
	const separator = " · "
	var out []string
	line := ""
	for _, label := range labels {
		switch {
		case line == "":
			line = label
		case lipgloss.Width(line+separator+label) > width:
			out = append(out, line)
			line = label
		default:
			line += separator + label
		}
	}
	return append(out, line)
}
