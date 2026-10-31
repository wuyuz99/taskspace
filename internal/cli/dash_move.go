package cli

import (
	"slices"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/wuyuz99/taskspace/internal/task"
	"github.com/wuyuz99/taskspace/internal/workspace"
)

// moveRepoForm moves repo out of old, as tspace repo move does. The cursor
// follows it to the new task.
func (m dashModel) moveRepoForm(old, repo string) *dashForm {
	var tasks []string
	for _, r := range m.data.Rows {
		if r.Task != old && !slices.Contains(tasks, r.Task) {
			tasks = append(tasks, r.Task)
		}
	}
	f := &dashForm{
		title: "move " + old + "/" + repo + " (tspace repo move; conversations stay at the old path unless carried)",
		fields: []dashField{
			dashField{label: "to", hint: "no other task to move it to"}.pick(tasks, true),
			{label: "name", hint: "name in that task (default: " + repo + ")"},
		},
		check: func(f *dashForm) string {
			if f.field("to").text() == "" {
				return "pick a task"
			}
			if name := f.field("name").text(); name != "" {
				if err := task.ValidateName("repo", name); err != nil {
					return err.Error()
				}
			}
			return ""
		},
	}
	f.submit = func(f *dashForm) tea.Cmd {
		to, name := f.field("to").text(), f.field("name").text()
		f.busy = "moving " + repo + "..."
		act := m.act(func(ws *workspace.Service) (string, error) {
			return movedRepoText(repo, old, to, name), ws.MoveRepo(m.ctx, old, repo, to, name)
		})
		return func() tea.Msg {
			msg := act().(dashDoneMsg)
			if msg.err == nil {
				msg.made = to
			}
			return msg
		}
	}
	return f
}
