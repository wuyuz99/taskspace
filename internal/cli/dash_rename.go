package cli

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/wuyuz99/taskspace/internal/task"
	"github.com/wuyuz99/taskspace/internal/workspace"
)

func (m dashModel) renameForm(old string) *dashForm {
	f := &dashForm{
		title:  "rename " + old + " (tspace task rename; directory-based conversations stay at the old path)",
		fields: []dashField{{label: "name", hint: "new task name"}, {label: "branches", hint: "rename unpublished default branches (--branches)", toggle: true}},
		check: func(f *dashForm) string {
			name := f.field("name").text()
			if err := task.ValidateName("task", name); err != nil {
				return err.Error()
			}
			if m.app.Store.Exists(name) {
				return fmt.Sprintf("task %s already exists", name)
			}
			return ""
		},
	}
	f.submit = func(f *dashForm) tea.Cmd {
		name, branches := f.field("name").text(), f.field("branches").on
		f.busy = "renaming " + old + "..."
		act := m.act(func(ws *workspace.Service) (string, error) {
			err := ws.Rename(m.ctx, old, name, workspace.RenameOptions{Branches: branches})
			return "renamed " + old + " to " + name, err
		})
		return func() tea.Msg {
			msg := act().(dashDoneMsg)
			if msg.err == nil {
				msg.made, msg.gone = name, old
			}
			return msg
		}
	}
	return f
}
