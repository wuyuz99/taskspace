package cli

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/wuyuz99/taskspace/internal/task"
)

// createForm is n's form: what `tspace task create NAME REPO... [--project
// P] [--start]` takes, checked as it is typed. Registered repositories and
// saved projects are picked; anything else is typed under more.
func (m dashModel) createForm() *dashForm {
	var repos, projects []string
	if c, err := m.app.Store.LoadConfig(); err == nil {
		repos = c.RepoNames()
	}
	if ps, err := m.app.Store.ListProjects(); err == nil {
		for _, p := range ps {
			projects = append(projects, p.Name)
		}
	}
	f := &dashForm{
		title: "new task (tspace task create)",
		fields: []dashField{
			{label: "name", hint: "letters, digits, '.', '_' or '-'"},
			dashField{label: "repositories", hint: "none registered (tspace config repo); type them under more"}.pick(repos, false),
			{label: "more", hint: "REPO[@BRANCH][:BASE] ...  a branch, a base or a path"},
			dashField{label: "project", hint: "none saved"}.pick(projects, true),
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
	if name == "" {
		return "name the task"
	}
	if err := task.ValidateName("task", name); err != nil {
		return err.Error()
	}
	if m.app.Store.Exists(name) {
		return fmt.Sprintf("task %s already exists", name)
	}
	repos := len(createRepos(f)) > 0
	if p := f.field("project").text(); p != "" {
		project, err := m.app.Store.LoadProject(p)
		if err != nil {
			return err.Error()
		}
		repos = repos || len(project.Repos) > 0
	}
	if f.field("start").on && !repos {
		return "starting needs a repository; name one, or untick start"
	}
	return ""
}

// create runs task create with what f holds, as typed, so the dash and the
// command cannot differ.
func (m dashModel) create(f *dashForm) tea.Cmd {
	name := f.field("name").text()
	var args []string
	if p := f.field("project").text(); p != "" {
		args = append(args, "--project", p)
	}
	start := f.field("start").on
	if start {
		args = append(args, "--start")
	}
	// After --, nothing typed is read as a flag.
	args = append(append(args, "--", name), createRepos(f)...)
	return func() tea.Msg {
		var out, warn bytes.Buffer
		sub := *m.app
		sub.Out, sub.Err, sub.hookOut = &out, &warn, io.Discard
		sub.noWait = true
		existed := m.app.Store.Exists(name)
		err := sub.runCmd(m.ctx, (*App).newTaskCreateCmd, args...)
		msg := dashDoneMsg{err: err}
		if existed || !m.app.Store.Exists(name) {
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

// createRepos are the repositories f names: those typed under more, and
// each picked one that is not typed there too, with a branch or a base.
func createRepos(f *dashForm) []string {
	typed := strings.Fields(f.field("more").text())
	named := map[string]bool{}
	for _, t := range typed {
		if s, err := parseRepoSpec(t); err == nil {
			named[s.Source] = true
		}
	}
	var out []string
	for _, r := range strings.Fields(f.field("repositories").text()) {
		if !named[r] {
			out = append(out, r)
		}
	}
	return append(out, typed...)
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
