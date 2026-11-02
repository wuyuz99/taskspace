package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/wuyuz99/taskspace/internal/fspath"
	"github.com/wuyuz99/taskspace/internal/task"
	"github.com/wuyuz99/taskspace/internal/workspace"
)

// registerRepoForm is a's form: what `tspace config repo NAME PATH` takes. A
// URL stays a command: a clone may have to ask for credentials on a terminal
// dash is holding.
func (m dashModel) registerRepoForm() *dashForm {
	f := &dashForm{
		title: "register a repository (tspace config repo)",
		fields: []dashField{
			{label: "path", hint: "a repository on this machine, such as ~/src/backend"},
			{label: "name", hint: "optional; the directory's name"},
		},
		check: func(f *dashForm) string {
			path := f.field("path").text()
			if path == "" {
				return "give the repository's path"
			}
			if looksLikeURL(path) && !fspath.IsDir(workspace.ExpandHome(path)) {
				return "dash takes a path; clone a URL with: tspace config repo NAME URL"
			}
			if !fspath.IsDir(workspace.ExpandHome(path)) {
				return "no such directory: " + path
			}
			name := repoFormName(f)
			if err := task.ValidateName("repo", name); err != nil {
				return err.Error() + "; give it a name"
			}
			if hint, verb := configRepoVerbs[name]; verb {
				return fmt.Sprintf("%q reads as a subcommand; %s", name, hint)
			}
			return ""
		},
	}
	f.submit = func(f *dashForm) tea.Cmd {
		name, path := repoFormName(f), f.field("path").text()
		f.busy = "registering " + name + "..."
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
			defer cancel()
			canonical, err := m.app.registerRepo(ctx, name, workspace.ExpandHome(path))
			if err != nil {
				return dashDoneMsg{err: err}
			}
			return dashDoneMsg{text: fmt.Sprintf("registered %s: %s", name, canonical)}
		}
	}
	return f
}

// repoFormName is the name typed, else the directory's.
func repoFormName(f *dashForm) string {
	if name := f.field("name").text(); name != "" {
		return name
	}
	return filepath.Base(filepath.Clean(workspace.ExpandHome(f.field("path").text())))
}

// dashWelcome is what an empty dash shows: the way to a first task, with the
// step already done marked.
func dashWelcome(repos int) string {
	first := "  1. press a to register a repository you work in (tspace config repo)"
	if repos > 0 {
		first = fmt.Sprintf("  1. done: %d repositor%s registered; press a to add another", repos, map[bool]string{true: "y", false: "ies"}[repos == 1])
	}
	return strings.Join([]string{
		"welcome to tspace: a task is one piece of work across your repositories",
		"",
		first,
		"  2. press n to create a task and pick its repositories (tspace task create)",
		"  3. press enter on it to work in its session",
		"",
	}, "\n")
}
