package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/wuyuz99/taskspace/internal/runtime"
	"github.com/wuyuz99/taskspace/internal/status"
	"github.com/wuyuz99/taskspace/internal/task"
)

func (h *harness) signals(t *testing.T, name string) map[string]task.Signal {
	t.Helper()
	got, err := h.app.Store.ReadSignals(name)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// A hook has no arguments to spare, so the directory says which window it is.
func TestSignalFindsItsWindowFromTheDirectory(t *testing.T) {
	h := newHarness(t)
	tk := h.startedTask(t, "PAY-142")
	repo := tk.RepoNames()[0]

	sub := filepath.Join(tk.Repos[repo].Worktree, "deep", "inside")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	chdir(t, sub)
	if err := h.exec(t, h.app.newSignalCmd(), "blocked", "needs a\ndecision"); err != nil {
		t.Fatal(err)
	}
	if got := h.signals(t, "PAY-142")[repo]; got.State != task.SignalBlocked || got.Message != "needs a decision" || got.At.IsZero() {
		t.Fatalf("worktree signal: %+v", got)
	}

	chdir(t, h.app.Store.ContextDir("PAY-142"))
	if err := h.exec(t, h.app.newSignalCmd(), "done"); err != nil {
		t.Fatal(err)
	}
	if got := h.signals(t, "PAY-142")[task.RootWindowName]; got.State != task.SignalDone {
		t.Fatalf("root signal: %+v", got)
	}

	chdir(t, t.TempDir())
	if err := h.exec(t, h.app.newSignalCmd(), "done"); err == nil || !strings.Contains(err.Error(), "--task") {
		t.Fatalf("outside a task: %v", err)
	}
	if err := h.exec(t, h.app.newSignalCmd(), "working", "--task", "PAY-142", "--repo", repo); err != nil {
		t.Fatal(err)
	}
	if got := h.signals(t, "PAY-142")[repo]; got.State != task.SignalWorking || got.Message != "" {
		t.Fatalf("named signal: %+v", got)
	}

	for _, bad := range [][]string{
		{"waiting", "--task", "PAY-142"},
		{"--task", "PAY-142"},
		{"done", "--task", "PAY-142", "--repo", "nope"},
		{"done", "--task", "PAY-142", "--repo", repo, "--root"},
		{"done", "--clear", "--task", "PAY-142"},
	} {
		if err := h.exec(t, h.app.newSignalCmd(), bad...); err == nil {
			t.Fatalf("%v accepted", bad)
		}
	}

	if err := h.exec(t, h.app.newSignalCmd(), "--clear", "--task", "PAY-142", "--repo", repo); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.signals(t, "PAY-142")[repo]; ok {
		t.Fatal("--clear left the signal")
	}
}

func TestStatusShowsSignalsOnlyWhenThereAreSome(t *testing.T) {
	h := newHarness(t)
	tk := h.startedTask(t, "PAY-142")
	repo := tk.RepoNames()[0]

	if err := h.exec(t, h.app.newStatusCmd()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(h.out.String(), "SIGNAL") {
		t.Fatalf("signal column with no signals:\n%s", h.out.String())
	}

	// Written the way an agent without tspace would write it.
	if err := os.MkdirAll(h.app.Store.SignalsDir("PAY-142"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(h.app.Store.SignalPath("PAY-142", repo), []byte(`{"state":"blocked","message":"which column?"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// A file nobody can read as a signal hides nothing else.
	if err := os.WriteFile(h.app.Store.SignalPath("PAY-142", "junk"), []byte(`{"state":"asleep"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := h.exec(t, h.app.newStatusCmd()); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(h.out.String()), "\n")
	if !strings.HasSuffix(lines[0], "SIGNAL") || !strings.HasSuffix(lines[1], " -") || !strings.Contains(lines[2], "blocked 0s") {
		t.Fatalf("status:\n%s", h.out.String())
	}

	if err := h.exec(t, h.app.newStatusCmd(), "--json"); err != nil {
		t.Fatal(err)
	}
	var got []statusJSON
	if err := json.Unmarshal(h.out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if s := got[0].Repos[0].Signal; s == nil || s.State != task.SignalBlocked || s.Message != "which column?" || got[0].Root.Signal != nil {
		t.Fatalf("json: %s", h.out.String())
	}
}

// A signal describes a running agent, so it does not outlive one.
func TestSignalsAreClearedWithTheSession(t *testing.T) {
	h := newHarness(t)
	tk := h.startedTask(t, "PAY-142")
	repo := tk.RepoNames()[0]
	set := func() {
		t.Helper()
		for _, w := range []string{repo, task.RootWindowName} {
			if err := h.app.Store.WriteSignal("PAY-142", w, task.SignalDone, ""); err != nil {
				t.Fatal(err)
			}
		}
	}

	set()
	if err := h.exec(t, h.app.newRestartCmd(), "PAY-142", repo); err != nil {
		t.Fatal(err)
	}
	if got := h.signals(t, "PAY-142"); len(got) != 1 || got[task.RootWindowName].State != task.SignalDone {
		t.Fatalf("after restart: %+v", got)
	}

	// In this order: clean runs last, so the directory check below holds.
	for _, c := range []struct {
		name string
		run  func() error
	}{
		{"stop", func() error { return h.exec(t, h.app.newStopCmd(), "PAY-142") }},
		{"start", func() error { return h.exec(t, h.app.newStartCmd(), "PAY-142") }},
		{"clean", func() error { return h.exec(t, h.app.newCleanCmd(), "PAY-142") }},
	} {
		set()
		if err := c.run(); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got := h.signals(t, "PAY-142"); len(got) != 0 {
			t.Fatalf("after %s: %+v", c.name, got)
		}
	}
	// With no notes either, clean leaves no task directory behind.
	if _, err := os.Stat(h.app.Store.TaskDir("PAY-142")); !os.IsNotExist(err) {
		t.Fatalf("task directory left behind: %v", err)
	}
}

func TestStatusWatchRedrawsUntilCancelled(t *testing.T) {
	h := newHarness(t)
	h.startedTask(t, "PAY-142")

	ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
	defer cancel()
	cmd := h.app.newStatusCmd()
	cmd.SetArgs([]string{"--watch", "--interval", "200ms"})
	if err := cmd.ExecuteContext(ctx); err != nil {
		t.Fatal(err)
	}
	// Not a terminal, so no escape sequences: just the table again.
	if n := strings.Count(h.out.String(), "RUNTIME"); n < 2 || strings.Contains(h.out.String(), "\033") {
		t.Fatalf("%d frames:\n%q", n, h.out.String())
	}
	if err := h.exec(t, h.app.newStatusCmd(), "--watch", "--json"); err == nil {
		t.Fatal("--watch --json accepted")
	}
}

func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// drive feeds a model one message and then whatever its command produced, the
// way the program would, without a terminal.
func drive(t *testing.T, m dashModel, msg tea.Msg) dashModel {
	t.Helper()
	next, cmd := m.Update(msg)
	m = next.(dashModel)
	if cmd == nil {
		return m
	}
	switch out := cmd().(type) {
	case dashDoneMsg, dashAttachMsg:
		next, _ = m.Update(out)
		m = next.(dashModel)
	}
	return m
}

func TestDash(t *testing.T) {
	h := newHarness(t)
	tk := h.startedTask(t, "PAY-142")
	repo := tk.RepoNames()[0]
	if err := h.exec(t, h.app.newTaskCmd(), "handoff", "PAY-142", "# Notes\n\nmigration half done"); err != nil {
		t.Fatal(err)
	}
	if err := h.app.Store.WriteSignal("PAY-142", repo, task.SignalBlocked, "which column?"); err != nil {
		t.Fatal(err)
	}
	rt := &windowRuntime{running: true, panes: map[string]runtime.PaneState{"_root": {Dead: true}, repo: {}}}
	h.app.Runtime = rt

	if err := h.exec(t, h.app.newDashCmd()); err == nil || !strings.Contains(err.Error(), "terminal") {
		t.Fatalf("dash without a terminal: %v", err)
	}

	m := newDashModel(context.Background(), h.app, time.Second)
	// Wide enough that the detail line keeps a long temp path whole.
	m = drive(t, m, tea.WindowSizeMsg{Width: 400, Height: 40})
	m = drive(t, m, dashDataMsg(h.app.dashLoad(context.Background())))
	if len(m.data.Rows) != 2 || m.data.Rows[0].Runtime != status.RuntimeDead || m.data.Rows[1].Signal == nil {
		t.Fatalf("rows: %+v", m.data.Rows)
	}
	view := m.View()
	for _, want := range []string{"1 task", "1 blocked", "1 dead", "PAY-142 (root)", "migration half done"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view lacks %q:\n%s", want, view)
		}
	}

	// A dead root can be restarted; a live window cannot.
	m = drive(t, m, key("r"))
	if m.noteErr || !strings.Contains(m.note, "relaunched PAY-142 root window") {
		t.Fatalf("restart: %q", m.note)
	}
	m = drive(t, m, key("down"))
	if view := m.View(); !strings.Contains(view, "blocked, 0s ago: which column?") || !strings.Contains(view, tk.Repos[repo].Worktree) {
		t.Fatalf("detail:\n%s", view)
	}
	m = drive(t, m, key("r"))
	if !strings.Contains(m.note, "only a dead window") {
		t.Fatalf("restart of a live window: %q", m.note)
	}

	// Inside a client that can show the window, enter switches to it.
	m = drive(t, m, key("enter"))
	if len(rt.focused) != 1 || rt.focused[0] != repo || m.attach != nil || !strings.Contains(m.note, "switched to PAY-142/"+repo) {
		t.Fatalf("focus: %v, %q", rt.focused, m.note)
	}

	// Stopping asks first, and anything but y is a no.
	m = drive(t, m, key("x"))
	if m.confirm == nil || !strings.Contains(m.View(), "stop PAY-142") {
		t.Fatalf("no confirmation:\n%s", m.View())
	}
	m = drive(t, m, key("n"))
	if m.confirm != nil || m.note != "cancelled" || rt.stopped {
		t.Fatalf("declined stop: %q, stopped=%v", m.note, rt.stopped)
	}

	m = drive(t, m, key("c"))
	if _, ok := h.signals(t, "PAY-142")[repo]; ok || !strings.Contains(m.note, "signal cleared") {
		t.Fatalf("clear: %q", m.note)
	}

	// From a plain terminal there is nothing to switch, so dash quits to attach.
	h.app.Runtime = &stubAttach{windowRuntime: rt}
	m = drive(t, m, key("enter"))
	if m.attach == nil || m.attach.Window != repo {
		t.Fatalf("attach: %+v", m.attach)
	}
}

// stubAttach is a runtime outside tmux: it cannot focus, only attach.
type stubAttach struct{ *windowRuntime }

func (s *stubAttach) Focus(context.Context, string, string) (bool, error) { return false, nil }

// chdir moves the test into dir and back out again afterwards.
func chdir(t *testing.T, dir string) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
}

// A root agent that cd's into a worktree still reports for the root window.
func TestSignalPrefersTheWindowItWasLaunchedIn(t *testing.T) {
	h := newHarness(t)
	tk := h.startedTask(t, "PAY-142")
	repo := tk.RepoNames()[0]
	chdir(t, tk.Repos[repo].Worktree)

	t.Setenv(envTask, "PAY-142")
	t.Setenv(envWindow, task.RootWindowName)
	if err := h.exec(t, h.app.newSignalCmd(), "blocked"); err != nil {
		t.Fatal(err)
	}
	got := h.signals(t, "PAY-142")
	if got[task.RootWindowName].State != task.SignalBlocked {
		t.Fatalf("root signal: %+v", got)
	}
	if _, ok := got[repo]; ok {
		t.Fatalf("the repo window got the root agent's signal: %+v", got)
	}

	// Explicit flags win over the environment.
	if err := h.exec(t, h.app.newSignalCmd(), "done", "--repo", repo); err != nil {
		t.Fatal(err)
	}
	if got := h.signals(t, "PAY-142")[repo]; got.State != task.SignalDone {
		t.Fatalf("--repo signal: %+v", got)
	}

	// A window the task no longer has, or no window at all, falls back to the directory.
	for _, window := range []string{"gone", ""} {
		t.Setenv(envWindow, window)
		if err := h.exec(t, h.app.newSignalCmd(), "working"); err != nil {
			t.Fatal(err)
		}
		if got := h.signals(t, "PAY-142")[repo]; got.State != task.SignalWorking {
			t.Fatalf("TSPACE_WINDOW=%q: %+v", window, got)
		}
		if err := h.app.Store.ClearSignal("PAY-142", repo); err != nil {
			t.Fatal(err)
		}
	}
}

func TestStartTellsEachWindowWhichItIs(t *testing.T) {
	h := newHarness(t)
	tk := h.startedTask(t, "PAY-142")
	if err := h.exec(t, h.app.newStartCmd(), tk.Name); err != nil {
		t.Fatal(err)
	}
	rt := h.app.Runtime.(*stubRuntime)
	want := map[string]bool{task.RootWindowName: true}
	for _, name := range tk.RepoNames() {
		want[name] = true
	}
	if len(rt.started) != len(want) {
		t.Fatalf("started %d windows, want %d", len(rt.started), len(want))
	}
	for _, w := range rt.started {
		env := strings.Join(w.Env, " ")
		if env != "TSPACE_TASK=PAY-142 TSPACE_WINDOW="+w.Name || !want[w.Name] {
			t.Errorf("window %s env = %q", w.Name, env)
		}
	}
}
