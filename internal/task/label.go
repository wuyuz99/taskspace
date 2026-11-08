package task

import (
	"strings"
	"unicode"
)

// labelTitleWidth caps the title in a label, which a status line or a
// terminal tab shows beside everything else it holds.
const labelTitleWidth = 48

// Label is what a task is shown as outside tspace, in its session's status
// line and the terminal tab: its name, and its title if it has one.
func (t *Task) Label() string { return Label(t.Name, t.Title) }

// Label joins name and title as "NAME - TITLE", or gives name alone for no
// title. The title is put on one line, stripped of control characters, which
// could end a terminal's title sequence early, and cut to labelTitleWidth.
func Label(name, title string) string {
	title = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, title)
	title = strings.Join(strings.Fields(title), " ")
	if title == "" {
		return name
	}
	if r := []rune(title); len(r) > labelTitleWidth {
		title = strings.TrimSpace(string(r[:labelTitleWidth-1])) + "…"
	}
	return name + " - " + title
}
