package cli

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// dashKey is one of dash's keys, as its help and legend describe it. key()
// does what it says; TestDashKeysAreHandled keeps the two together.
type dashKey struct {
	// keys is how the help names it, and press is a key to test it with.
	keys, press string
	// help is its line in `tspace dash --help`.
	help string
	// legend is its entry at the bottom of the screen, if it has one.
	legend string
}

// dashKeys are listed in the order of the help and the legend.
var dashKeys = []dashKey{
	{keys: "up/down, j/k", press: "j", help: "select a row (mouse wheel moves one row)"},
	{keys: "enter", press: "enter", legend: "enter attach", help: "attach to that window; detaching returns here"},
	{keys: "space", press: " ", legend: "space expand", help: "expand/collapse a task; one that needs you stays expanded"},
	{keys: "s", press: "s", legend: "s/x task", help: "start the task (tspace start)"},
	{keys: "x", press: "x", help: "stop the task, after confirming (tspace stop)"},
	{keys: "S / X", press: "S", legend: "S/X window", help: "start / stop only the selected window (tspace start/stop TASK REPO)"},
	{keys: "r", press: "r", legend: "r restart", help: "restart a dead window (tspace restart)"},
	{keys: "D", press: "D", legend: "D clean", help: "clean the task, after confirming (tspace clean)"},
	{keys: "R", press: "R", legend: "R delete", help: "delete the task, after confirming (tspace clean --rm)"},
	{keys: "-", press: "-", legend: "- remove repo", help: "remove the selected repository from the task, after\nconfirming (tspace repo rm)"},
	{keys: "m", press: "m", legend: "m move repo", help: "move the selected repository to another task, in a form\n(tspace repo move)"},
	{keys: "t", press: "t", legend: "t tab", help: "open the task in a new terminal tab (tspace config terminal)"},
	{keys: "c", press: "c", legend: "c clear", help: "clear the row's signal"},
	{keys: "n", press: "n", legend: "n new", help: "create a task, in a form below the table (tspace task create)"},
	{keys: "N", press: "N", legend: "N rename", help: "rename the selected task (tspace task rename)"},
	{keys: "w", press: "w", legend: "w window", help: "open another window at the row's place, in a form: a shell,\nthe task's agent or a command (tspace window -d)"},
	{keys: "W", press: "W", legend: "W save window", help: "save the selected open extra window (tspace window --save)"},
	{keys: "I", press: "I", legend: "I interrupted", help: "start every interrupted task (tspace start --interrupted);\ndash offers it on launch when there are any"},
	{keys: "q", press: "q", legend: "q quit", help: "quit"},
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

// dashLegend is every key's legend entry, wrapped to width columns, so that
// none is lost to the edge of the screen.
func dashLegend(width int) []string {
	const separator = " · "
	var out []string
	line := ""
	for _, k := range dashKeys {
		switch {
		case k.legend == "":
		case line == "":
			line = k.legend
		case lipgloss.Width(line+separator+k.legend) > width:
			out = append(out, line)
			line = k.legend
		default:
			line += separator + k.legend
		}
	}
	return append(out, line)
}
