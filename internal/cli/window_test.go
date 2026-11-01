package cli

import (
	"slices"
	"strings"
	"testing"

	"github.com/wuyuz99/taskspace/internal/runtime"
	"github.com/wuyuz99/taskspace/internal/runtime/runtimetest"
)

// clientInside is a multiplexer with tspace running inside a client of it.
func clientInside() *runtimetest.Fake {
	rt := runtimetest.New()
	rt.ClientInside(true)
	return rt
}

// runningSession is that, with task's session running these windows.
func runningSession(task string, panes map[string]runtime.PaneState) *runtimetest.Fake {
	rt := clientInside()
	rt.Seed(rt.SessionName(task), panes)
	return rt
}

func opened(rt *runtimetest.Fake) []runtime.Window {
	var out []runtime.Window
	for _, c := range rt.Calls(runtimetest.OpOpenWindow) {
		out = append(out, c.Window)
	}
	return out
}

func focused(rt *runtimetest.Fake) []string {
	var out []string
	for _, c := range rt.Calls(runtimetest.OpFocus) {
		out = append(out, c.Window.Name)
	}
	return out
}

func stopped(rt *runtimetest.Fake) bool { return len(rt.Calls(runtimetest.OpStop)) > 0 }

func TestWindowOpensBesideExistingWindows(t *testing.T) {
	h := newHarness(t)
	other := mkRepo(t)
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-9", "--agent", "claude", "--repo", "backend="+h.repo, "--repo", "frontend="+other); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newStartCmd(), "PAY-9", "--no-runtime"); err != nil {
		t.Fatal(err)
	}
	rt := clientInside()
	h.app.Runtime = rt
	tk, _ := h.app.Store.Load("PAY-9")

	if err := h.exec(t, h.app.newWindowCmd(), "PAY-9", "backend"); err == nil || !strings.Contains(err.Error(), "tspace open PAY-9") {
		t.Fatalf("window without a session = %v, want a hint to open it", err)
	}
	rt.Seed(rt.SessionName("PAY-9"), map[string]runtime.PaneState{"_root": {}, "backend": {}, "frontend": {}})

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
		{Name: "_backend-claude", Dir: tk.Repos["backend"].Worktree, Command: []string{"claude"}},
		{Name: "_frontend-npm", Dir: tk.Repos["frontend"].Worktree, Command: []string{"npm", "run", "dev"}},
		{Name: "_frontend-npm-2", Dir: tk.Repos["frontend"].Worktree, Command: []string{"npm", "run", "dev"}},
		{Name: "_staging", Dir: h.app.Store.TaskDir("PAY-9"), Transient: true},
	}
	if len(opened(rt)) != len(want) {
		t.Fatalf("opened %+v", opened(rt))
	}
	for i, w := range want {
		got := opened(rt)[i]
		if got.Name != w.Name || got.Dir != w.Dir || !slices.Equal(got.Command, w.Command) || got.Transient != w.Transient {
			t.Errorf("window %d = %+v, want %+v", i, got, w)
		}
	}
	if !slices.Equal(focused(rt), []string{"_backend-claude", "_frontend-npm", "_frontend-npm-2"}) {
		t.Errorf("focused %v; -d must not switch", focused(rt))
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
	if len(opened(rt)) != len(want) {
		t.Errorf("a refused window was opened: %+v", opened(rt)[len(want):])
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
	rt := runningSession("PAY-10", nil)
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
	if got := opened(rt)[0]; got.Name != "_backend-codex" || !slices.Equal(got.Command, []string{"codex"}) {
		t.Errorf("opened %+v", got)
	}
}
