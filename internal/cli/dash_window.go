package cli

import (
	"bytes"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/wuyuz99/taskspace/internal/status"
	"github.com/wuyuz99/taskspace/internal/task"
)

// windowForm is w's form: what `tspace window TASK [REPO | --root] -d
// [--agent | -- COMMAND] [--name N --save]` takes, opening at row's place.
func (m dashModel) windowForm(row dashRow) *dashForm {
	place, where := "--root", "the task root"
	if repo := m.windowPlace(row); repo != "" {
		place, where = repo, repo+"'s worktree"
	}
	f := &dashForm{
		title: fmt.Sprintf("new window in %s, at %s (tspace window)", row.Task, where),
		fields: []dashField{
			{label: "command", hint: "empty for a shell, agent for the task's agent"},
			{label: "name", hint: "optional; shown as _NAME"},
			{label: "save", hint: "start opens it again (--save, needs a name)", toggle: true},
		},
		check: func(f *dashForm) string {
			name := strings.TrimPrefix(f.field("name").text(), task.ExtraWindowPrefix)
			if name != "" {
				if err := task.ValidateName("window", name); err != nil {
					return err.Error()
				}
			}
			if f.field("save").on && name == "" {
				return "saving needs a name, which start reopens it by"
			}
			if _, err := splitWords(f.field("command").text()); err != nil {
				return err.Error()
			}
			return ""
		},
	}
	f.submit = func(f *dashForm) tea.Cmd {
		f.busy = "opening a window in " + row.Task + "..."
		return m.openWindow(row.Task, place, f)
	}
	return f
}

// windowPlace is the repository a window opened from row goes in, or ""
// for the task root: a repository row's own, a saved extra window's, and
// otherwise the root.
func (m dashModel) windowPlace(row dashRow) string {
	switch {
	case row.Summary > 1 || row.root():
		return ""
	case row.extra():
		if t, err := m.app.Store.Load(row.Task); err == nil {
			if i, ok := t.SavedWindow(row.Window); ok {
				return t.Windows[i].Repo
			}
		}
		return ""
	}
	return row.Window
}

// sessionUp says the task's session exists, as its rows show it.
func (m dashModel) sessionUp(name string) bool {
	for _, r := range m.rawRows {
		if r.Task == name && r.Runtime != status.RuntimeNoSession && r.Runtime != status.RuntimeUnknown {
			return true
		}
	}
	return false
}

// openWindow runs `tspace window` with what f holds, detached so dash stays
// in front; the cursor then goes to the new window.
func (m dashModel) openWindow(taskName, place string, f *dashForm) tea.Cmd {
	args := []string{taskName, place, "-d"}
	if name := strings.TrimPrefix(f.field("name").text(), task.ExtraWindowPrefix); name != "" {
		args = append(args, "--name", name)
	}
	if f.field("save").on {
		args = append(args, "--save")
	}
	switch command := f.field("command").text(); command {
	case "":
	case "agent":
		args = append(args, "--agent")
	default:
		argv, _ := splitWords(command) // checked as it was typed
		args = append(append(args, "--"), argv...)
	}
	return func() tea.Msg {
		var out, warn bytes.Buffer
		sub := *m.app
		sub.Out, sub.Err = &out, &warn
		sub.noWait = true
		err := sub.runCmd(m.ctx, (*App).newWindowCmd, args...)
		if err != nil {
			return dashDoneMsg{err: err}
		}
		// "opened _NAME in TASK: ..." names the window it chose.
		text := lines(out.String())
		msg := dashDoneMsg{text: strings.Join(text, "; ")}
		if len(text) > 0 {
			if w, ok := strings.CutPrefix(text[0], "opened "); ok {
				msg.made, msg.window = taskName, strings.Fields(w)[0]
			}
		}
		return msg
	}
}

// splitWords splits a command line typed into dash into argv as a POSIX
// shell would split its words: on blanks, with '...' taken literally and
// "..." and backslash escaping. Nothing is expanded; tspace runs no shell.
func splitWords(line string) ([]string, error) {
	var words []string
	var word strings.Builder
	in, quote, escaped := false, rune(0), false
	for _, r := range line {
		switch {
		case escaped:
			word.WriteRune(r)
			escaped = false
		case quote == '\'':
			if r == '\'' {
				quote = 0
			} else {
				word.WriteRune(r)
			}
		case r == '\\' && quote == 0, r == '\\' && quote == '"':
			escaped = true
		case quote == '"':
			if r == '"' {
				quote = 0
			} else {
				word.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote, in = r, true
		case r == ' ' || r == '\t':
			if in {
				words = append(words, word.String())
				word.Reset()
				in = false
			}
		default:
			word.WriteRune(r)
			in = true
		}
		if escaped {
			in = true
		}
	}
	if quote != 0 || escaped {
		return nil, fmt.Errorf("unfinished quote or escape in %q", line)
	}
	if in {
		words = append(words, word.String())
	}
	return words, nil
}
