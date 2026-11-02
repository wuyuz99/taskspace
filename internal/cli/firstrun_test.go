package cli

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func typed(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

// Output that is not a terminal gets the help, as a mistaken script would.
func TestBareTspaceOutsideATerminalShowsHelp(t *testing.T) {
	h := newHarness(t)
	root := h.app.newRootCmd()
	root.SetOut(h.out)
	root.SetArgs(nil)
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.out.String(), "Usage:") {
		t.Fatalf("output: %q", h.out.String())
	}
}

func TestBareTspaceRefusesAMistypedCommand(t *testing.T) {
	h := newHarness(t)
	root := h.app.newRootCmd()
	root.SetOut(h.out)
	root.SetErr(h.errb)
	root.SetArgs([]string{"strat"})
	if err := root.ExecuteContext(context.Background()); err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("err = %v", err)
	}
}

// The way through an empty dash: register a repository in a form, and the
// welcome moves on to creating a task, whose form then offers it.
func TestFirstRunThroughDash(t *testing.T) {
	h := newHarness(t)
	m := newDashModel(context.Background(), h.app, time.Second)
	load := func() {
		m = drive(t, m, dashDataMsg(h.app.dashLoad(context.Background())))
	}
	load()
	if view := ansi.Strip(m.View()); !strings.Contains(view, "press a to register") {
		t.Fatalf("empty dash:\n%s", view)
	}

	m = drive(t, m, key("a"))
	if m.form == nil {
		t.Fatal("a opened no form")
	}
	m = drive(t, m, typed(h.repo))
	if m.form.note != "" {
		t.Fatalf("form says: %s", m.form.note)
	}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(dashModel)
	if cmd == nil {
		t.Fatal("enter did not submit")
	}
	msg := cmd()
	if done := msg.(dashDoneMsg); done.err != nil {
		t.Fatal(done.err)
	}
	m = drive(t, m, msg)
	load()

	c, err := h.app.Store.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Base(h.repo)
	if _, ok := c.Repos[name]; !ok {
		t.Fatalf("registered: %v", c.Repos)
	}
	if view := ansi.Strip(m.View()); !strings.Contains(view, "done: 1 repository registered") {
		t.Fatalf("after registering:\n%s", view)
	}
	m = drive(t, m, key("n"))
	if f := m.form; f == nil || len(f.field("repositories").choices) != 1 {
		t.Fatalf("create form: %+v", m.form)
	}
}

func TestRegisterRepoFormRefuses(t *testing.T) {
	h := newHarness(t)
	m := newDashModel(context.Background(), h.app, time.Second)
	m = drive(t, m, dashDataMsg(h.app.dashLoad(context.Background())))
	for _, c := range []struct{ path, want string }{
		{"", "give the repository's path"},
		{"/no/such/place", "no such directory"},
		{"git@github.com:o/r", "tspace config repo NAME URL"},
	} {
		open := drive(t, m, key("a"))
		if c.path != "" {
			open = drive(t, open, typed(c.path))
		}
		next, cmd := open.Update(tea.KeyMsg{Type: tea.KeyEnter})
		if cmd != nil {
			t.Fatalf("%q submitted", c.path)
		}
		if got := next.(dashModel).form.note; !strings.Contains(got, c.want) {
			t.Fatalf("%q: note %q", c.path, got)
		}
	}
}

// A second name for one repository is refused here as on the command line.
func TestRegisterRepoRefusesARepositoryTwice(t *testing.T) {
	h := newHarness(t)
	if _, err := h.app.registerRepo(context.Background(), "one", h.repo); err != nil {
		t.Fatal(err)
	}
	if _, err := h.app.registerRepo(context.Background(), "two", h.repo); err == nil || !strings.Contains(err.Error(), "already registered") {
		t.Fatalf("err = %v", err)
	}
}
