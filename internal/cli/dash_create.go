package cli

import (
	"bytes"
	"fmt"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/wuyuz99/taskspace/internal/task"
)

// createForm is n's form: what `tspace task create NAME REPO... [--project
// P] [--start]` takes, checked as it is typed.
func (m dashModel) createForm() *dashForm {
	repoHint, projectHint := "REPO[@BRANCH][:BASE] ...", "optional"
	if c, err := m.app.Store.LoadConfig(); err == nil && len(c.Repos) > 0 {
		names := make([]string, 0, len(c.Repos))
		for name := range c.Repos {
			names = append(names, name)
		}
		slices.Sort(names)
		repoHint += "  registered: " + strings.Join(names, " ")
	}
	if ps, err := m.app.Store.ListProjects(); err == nil && len(ps) > 0 {
		names := make([]string, len(ps))
		for i, p := range ps {
			names[i] = p.Name
		}
		projectHint += "; saved: " + strings.Join(names, " ")
	}
	f := &dashForm{
		title: "new task (tspace task create)",
		fields: []dashField{
			{label: "name", hint: "letters, digits, '.', '_' or '-'"},
			{label: "repositories", hint: repoHint},
			{label: "project", hint: projectHint},
			{label: "start", hint: "start it once it is created (--start)", toggle: true, on: true},
		},
		check: m.checkCreate,
	}
	f.submit = func(f *dashForm) tea.Cmd {
		f.busy = "creating " + f.field("name").text() + "..."
		return m.create(f)
	}
	return f
}

// checkCreate is what is wrong with f before anything is written.
func (m dashModel) checkCreate(f *dashForm) string {
	name := f.field("name").text()
	switch {
	case name == "":
		return "name the task"
	case task.ValidateName("task", name) != nil:
		return task.ValidateName("task", name).Error()
	case m.app.Store.Exists(name):
		return fmt.Sprintf("task %s already exists", name)
	case f.field("start").on && f.field("repositories").text() == "" && f.field("project").text() == "":
		return "starting needs a repository; name one, or untick start"
	}
	return ""
}

// create runs task create with what f holds, as typed, so the dash and the
// command cannot differ.
func (m dashModel) create(f *dashForm) tea.Cmd {
	name := f.field("name").text()
	args := append([]string{name}, strings.Fields(f.field("repositories").text())...)
	if p := f.field("project").text(); p != "" {
		args = append(args, "--project", p)
	}
	start := f.field("start").on
	if start {
		args = append(args, "--start")
	}
	return func() tea.Msg {
		var out, warn bytes.Buffer
		sub := *m.app
		sub.Out, sub.Err = &out, &warn
		sub.noWait = true
		err := sub.runCmd(m.ctx, (*App).newTaskCreateCmd, args...)
		msg := dashDoneMsg{err: err}
		if !m.app.Store.Exists(name) {
			return msg
		}
		// Created, even if starting it then failed: the cursor goes to it.
		msg.made = name
		msg.text = "created task " + name
		if start && err == nil {
			msg.text += "; " + lastLine(out.String())
		}
		if notes := lines(warn.String()); len(notes) > 0 && err == nil {
			msg.text += "; " + strings.Join(notes, "; ")
		}
		return msg
	}
}

// lines are s's non-blank lines, each made one.
func lines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, oneLine(l))
		}
	}
	return out
}

func lastLine(s string) string {
	ls := lines(s)
	if len(ls) == 0 {
		return ""
	}
	return ls[len(ls)-1]
}
