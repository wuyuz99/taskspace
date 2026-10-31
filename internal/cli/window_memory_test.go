package cli

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wuyuz99/taskspace/internal/runtime"
)

// memoryRuntime is a live session that also records what restart relaunched.
type memoryRuntime struct {
	liveRuntime
	restarted []runtime.Window
}

func (r *memoryRuntime) Restart(_ context.Context, _ string, w runtime.Window) error {
	r.restarted = append(r.restarted, w)
	return nil
}

func (r *memoryRuntime) CloseWindow(_ context.Context, _ string, window string) error {
	delete(r.panes, window)
	return nil
}

func (r *memoryRuntime) Stop(context.Context, string) error {
	r.panes = nil
	return nil
}

// memoryTask is a running task with one repository.
func memoryTask(t *testing.T) (*harness, *memoryRuntime, string) {
	t.Helper()
	h := newHarness(t)
	rt := &memoryRuntime{}
	h.app.Runtime = rt
	if err := h.exec(t, h.app.newTaskCmd(), "create", "T", "--repo", "backend="+h.repo, "--with-root"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newStartCmd(), "T"); err != nil {
		t.Fatal(err)
	}
	return h, rt, "backend"
}

func TestRestartUnsavedWindow(t *testing.T) {
	h, rt, repo := memoryTask(t)
	if err := h.exec(t, h.app.newWindowCmd(), "T", repo, "-d", "--", "lazygit", "{task_dir}"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newRestartCmd(), "T", "_backend-lazygit"); err != nil {
		t.Fatal(err)
	}
	if len(rt.restarted) != 1 {
		t.Fatalf("restarted %+v", rt.restarted)
	}
	got, opened := rt.restarted[0], rt.panes["_backend-lazygit"]
	if got.Dir != h.load(t, "T").Repos[repo].Worktree || got.Dir != opened.Dir || !slices.Equal(got.Command, opened.Command) ||
		!slices.Equal(got.Command, []string{"lazygit", h.app.Store.TaskDir("T")}) {
		t.Fatalf("restarted %+v, opened %+v", got, opened)
	}
	if got := h.load(t, "T").Windows; got != nil {
		t.Fatalf("an unsaved window reached the record: %+v", got)
	}
}

// A repository may have any name, so nothing but --root means the root.
func TestRememberedWindowInRepoNamedRoot(t *testing.T) {
	h := newHarness(t)
	rt := &memoryRuntime{}
	h.app.Runtime = rt
	if err := h.exec(t, h.app.newTaskCmd(), "create", "T", "--repo", "root="+h.repo, "--with-root"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newStartCmd(), "T"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newWindowCmd(), "T", "root", "-d", "--name", "x", "--", "top"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newRestartCmd(), "T", "_x"); err != nil {
		t.Fatal(err)
	}
	if got, want := rt.restarted[0].Dir, h.load(t, "T").Repos["root"].Worktree; got != want {
		t.Fatalf("restarted in %s, want %s", got, want)
	}
}

func TestSaveOpenWindow(t *testing.T) {
	h, rt, repo := memoryTask(t)
	window := func(args ...string) error {
		return h.exec(t, h.app.newWindowCmd(), append([]string{"T"}, args...)...)
	}
	if err := window(repo, "-d", "--", "lazygit"); err != nil {
		t.Fatal(err)
	}
	if err := window("--root", "-d", "--name", "scratch"); err != nil {
		t.Fatal(err)
	}
	before := len(rt.panes)

	// With or without the prefix, and the window is left as it is.
	if err := window("--save", "_backend-lazygit"); err != nil {
		t.Fatal(err)
	}
	if err := window("--save", "scratch"); err != nil {
		t.Fatal(err)
	}
	if len(rt.panes) != before {
		t.Fatalf("saving changed the session: %v", rt.panes)
	}
	tk := h.load(t, "T")
	if len(tk.Windows) != 2 || tk.Windows[0].Name != "backend-lazygit" || tk.Windows[0].Repo != repo ||
		!slices.Equal(tk.Windows[0].Command, []string{"lazygit"}) || tk.Windows[1].Repo != "" || len(tk.Windows[1].Command) != 0 {
		t.Fatalf("saved %+v", tk.Windows)
	}
	if _, ok := h.app.Store.OpenedWindow("T", "scratch"); ok {
		t.Fatal("a saved window is still remembered as unsaved")
	}

	if err := window("--save", "scratch"); err == nil || !strings.Contains(err.Error(), "already saved") {
		t.Fatalf("saved twice: %v", err)
	}
	if err := window("--save", "nope"); err == nil || !strings.Contains(err.Error(), "no open window _nope") {
		t.Fatalf("saved a window that is not open: %v", err)
	}
	if err := window("--save", "root"); err == nil {
		t.Fatal("saved the root window")
	}
	// Opened behind tspace's back: the error shows the way that works.
	rt.panes["_hand"] = runtime.Window{Name: "_hand"}
	if err := window("--save", "_hand"); err == nil || !strings.Contains(err.Error(), "--name hand --save -- COMMAND") {
		t.Fatalf("saved an unknown window: %v", err)
	}
	// Other flags mean opening one, which needs a name.
	if err := window(repo, "--save", "-d"); err == nil || !strings.Contains(err.Error(), "--save needs --name") {
		t.Fatalf("--save -d: %v", err)
	}
}

// The last primary window closing leaves the session up around its extra
// windows, which must not be forgotten with the record of what was running.
func TestExtraWindowOutlivesLastPrimary(t *testing.T) {
	h := newHarness(t)
	rt := &memoryRuntime{}
	h.app.Runtime = rt
	if err := h.exec(t, h.app.newTaskCmd(), "create", "T", "--repo", "backend="+h.repo); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newStartCmd(), "T"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newWindowCmd(), "T", "backend", "-d", "--name", "mon", "--", "top"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newStopCmd(), "T", "backend"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newRestartCmd(), "T", "_mon"); err != nil {
		t.Fatal(err)
	}
	if h.app.Store.Interrupted("T") {
		t.Fatal("only an extra window left, and offered as interrupted")
	}
}

func TestRenamedWindowKeepsItsCommand(t *testing.T) {
	h, rt, repo := memoryTask(t)
	if err := h.exec(t, h.app.newWindowCmd(), "T", repo, "-d", "--name", "a", "--", "top"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newWindowCmd(), "T", "--rename", "a", "b"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newRestartCmd(), "T", "_b"); err != nil {
		t.Fatal(err)
	}
	if w := rt.restarted[0]; w.Name != "_b" || !slices.Equal(w.Command, []string{"top"}) {
		t.Fatalf("restarted %+v", w)
	}
	if _, ok := h.app.Store.OpenedWindow("T", "a"); ok {
		t.Fatal("the old name is still remembered")
	}
}

// Closing a window through tspace, or ending its session, forgets it. A window
// that exits on its own is forgotten when the session goes.
func TestRememberedWindowEndsWithIt(t *testing.T) {
	h, rt, repo := memoryTask(t)
	open := func(name string) {
		t.Helper()
		if err := h.exec(t, h.app.newWindowCmd(), "T", repo, "-d", "--name", name, "--", "top"); err != nil {
			t.Fatal(err)
		}
	}
	open("a")
	open("b")
	if err := h.exec(t, h.app.newStopCmd(), "T", "_a"); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.app.Store.OpenedWindow("T", "a"); ok {
		t.Fatal("closed window still remembered")
	}
	if _, ok := h.app.Store.OpenedWindow("T", "b"); !ok {
		t.Fatal("closing one window forgot another")
	}

	if err := h.exec(t, h.app.newStopCmd(), "T"); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.app.Store.OpenedWindow("T", "b"); ok {
		t.Fatal("stop left a window remembered")
	}

	// An interruption records no end; the next session starts clean.
	if err := h.exec(t, h.app.newStartCmd(), "T"); err != nil {
		t.Fatal(err)
	}
	open("c")
	rt.panes = nil
	if err := h.exec(t, h.app.newStartCmd(), "T"); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.app.Store.OpenedWindow("T", "c"); ok {
		t.Fatal("a new session inherited a window")
	}
	if _, ok := rt.panes["_c"]; ok {
		t.Fatal("start opened an unsaved window")
	}
}

func TestProjectRecipeSkipsUnsavedWindows(t *testing.T) {
	h, _, repo := memoryTask(t)
	if err := h.exec(t, h.app.newWindowCmd(), "T", repo, "-d", "--name", "a", "--", "top"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newProjectCmd(), "save", "recipe", "--from", "T"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newTaskCmd(), "create", "U", "--project", "recipe"); err != nil {
		t.Fatal(err)
	}
	if got := h.load(t, "U").Windows; got != nil {
		t.Fatalf("recipe carried %+v", got)
	}
}

func TestDashSavesOpenWindow(t *testing.T) {
	h, rt, repo := memoryTask(t)
	if err := h.exec(t, h.app.newWindowCmd(), "T", repo, "-d", "--name", "mon", "--", "top"); err != nil {
		t.Fatal(err)
	}
	rt.panes["_hand"] = runtime.Window{Name: "_hand"}
	m := newDashModel(context.Background(), h.app, time.Second)
	m.expanded = map[string]bool{"T": true}
	m = drive(t, m, dashDataMsg(h.app.dashLoad(context.Background())))
	at := func(window string) int {
		for i, r := range m.data.Rows {
			if r.Window == window {
				return i
			}
		}
		t.Fatalf("no %s row: %+v", window, m.data.Rows)
		return -1
	}

	m.cursor = at(repo)
	m = drive(t, m, key("W"))
	if !strings.Contains(m.note, "saves an extra window") {
		t.Fatalf("W on a repository: %q", m.note)
	}
	m.cursor = at("_hand")
	m = drive(t, m, key("W"))
	if !m.noteErr || !strings.Contains(m.note, "does not know what _hand runs") {
		t.Fatalf("W on an unknown window: %q", m.note)
	}
	m.cursor = at("_mon")
	m = drive(t, m, key("W"))
	if m.noteErr {
		t.Fatal(m.note)
	}
	if _, ok := h.load(t, "T").SavedWindow("mon"); !ok {
		t.Fatal("_mon not saved")
	}
	m.cursor = at("_mon")
	m = drive(t, m, key("W"))
	if !strings.Contains(m.note, "already saved") {
		t.Fatalf("W on a saved window: %q", m.note)
	}
}
