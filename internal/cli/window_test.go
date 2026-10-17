package cli

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/wuyuz99/taskspace/internal/runtime"
)

// windowRuntime is a running session whose windows are whatever was opened.
type windowRuntime struct {
	stubRuntime
	running bool
	panes   map[string]runtime.PaneState
	opened  []runtime.Window
	focused []string
}

func (r *windowRuntime) State(context.Context, string) (runtime.SessionState, error) {
	return runtime.SessionState{Exists: r.running, Panes: r.panes}, nil
}
func (r *windowRuntime) OpenWindow(_ context.Context, _ string, w runtime.Window) error {
	r.opened = append(r.opened, w)
	r.panes[w.Name] = runtime.PaneState{Window: w.Name}
	return nil
}
func (r *windowRuntime) Focus(_ context.Context, _, window string) (bool, error) {
	r.focused = append(r.focused, window)
	return true, nil
}

func TestWindowOpensBesideExistingWindows(t *testing.T) {
	h := newHarness(t)
	other := mkRepo(t)
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-9", "--agent", "claude", "--repo", "backend="+h.repo, "--repo", "frontend="+other); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newStartCmd(), "PAY-9", "--no-runtime"); err != nil {
		t.Fatal(err)
	}
	rt := &windowRuntime{panes: map[string]runtime.PaneState{"_root": {}, "backend": {}, "frontend": {}}}
	h.app.Runtime = rt
	tk, _ := h.app.Store.Load("PAY-9")

	if err := h.exec(t, h.app.newWindowCmd(), "PAY-9", "backend"); err == nil || !strings.Contains(err.Error(), "tspace open PAY-9") {
		t.Fatalf("window without a session = %v, want a hint to open it", err)
	}
	rt.running = true

	// The task's agent in one repository, next to that repository's shell.
	if err := h.exec(t, h.app.newWindowCmd(), "PAY-9", "backend", "--agent"); err != nil {
		t.Fatal(err)
	}
	// A command, and the same again: the second gets its own name.
	for range 2 {
		if err := h.exec(t, h.app.newWindowCmd(), "PAY-9", "frontend", "--", "npm", "run", "dev"); err != nil {
			t.Fatal(err)
		}
	}
	// A spare named shell at the root, without switching to it.
	if err := h.exec(t, h.app.newWindowCmd(), "PAY-9", "--name", "staging", "-d"); err != nil {
		t.Fatal(err)
	}

	want := []runtime.Window{
		{Name: "_backend-claude", Dir: tk.Repos["backend"].Worktree, Command: []string{"claude", "--add-dir", h.app.Store.ContextDir("PAY-9")}},
		{Name: "_frontend-npm", Dir: tk.Repos["frontend"].Worktree, Command: []string{"npm", "run", "dev"}},
		{Name: "_frontend-npm-2", Dir: tk.Repos["frontend"].Worktree, Command: []string{"npm", "run", "dev"}},
		{Name: "_staging", Dir: h.app.Store.TaskDir("PAY-9"), Transient: true},
	}
	if len(rt.opened) != len(want) {
		t.Fatalf("opened %+v", rt.opened)
	}
	for i, w := range want {
		got := rt.opened[i]
		if got.Name != w.Name || got.Dir != w.Dir || !slices.Equal(got.Command, w.Command) || got.Transient != w.Transient {
			t.Errorf("window %d = %+v, want %+v", i, got, w)
		}
	}
	if !slices.Equal(rt.focused, []string{"_backend-claude", "_frontend-npm", "_frontend-npm-2"}) {
		t.Errorf("focused %v; -d must not switch", rt.focused)
	}

	for _, bad := range [][]string{
		{"PAY-9", "--name", "staging"},              // taken
		{"PAY-9", "backend", "--root"},              // two places
		{"PAY-9", "backend", "--agent", "--", "sh"}, // two commands
		{"PAY-9", "backend", "--"},                  // no command
		{"PAY-9", "nope"},                           // no such repo
	} {
		if err := h.exec(t, h.app.newWindowCmd(), bad...); err == nil {
			t.Errorf("window %v accepted", bad)
		}
	}
	if len(rt.opened) != len(want) {
		t.Errorf("a refused window was opened: %+v", rt.opened[len(want):])
	}
}

func TestWindowAgentNeedsOne(t *testing.T) {
	h := newHarness(t)
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-10", "--shell", "--repo", "backend="+h.repo); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newStartCmd(), "PAY-10", "--no-runtime"); err != nil {
		t.Fatal(err)
	}
	rt := &windowRuntime{running: true, panes: map[string]runtime.PaneState{}}
	h.app.Runtime = rt
	if err := h.exec(t, h.app.newWindowCmd(), "PAY-10", "--agent"); err == nil || !strings.Contains(err.Error(), "tspace config agent") {
		t.Fatalf("--agent with none configured = %v", err)
	}
	// A shell-root task still reaches the user's default agent.
	if err := h.exec(t, h.app.newConfigCmd(), "agent", "codex"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newWindowCmd(), "PAY-10", "--agent"); err != nil {
		t.Fatal(err)
	}
	// One repository: the window opens in its worktree.
	if got := rt.opened[0]; got.Name != "_backend-codex" || !slices.Equal(got.Command, []string{"codex"}) {
		t.Errorf("opened %+v", got)
	}
}
