package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/wuyuz99/taskspace/internal/runtime"
	"github.com/wuyuz99/taskspace/internal/status"
	"github.com/wuyuz99/taskspace/internal/task"
)

// restartingRuntime records what restart relaunched.
type restartingRuntime struct {
	layoutRuntime
	restarted []runtime.Window
}

func (r *restartingRuntime) Restart(_ context.Context, _ string, w runtime.Window) error {
	r.restarted = append(r.restarted, w)
	return nil
}

// extraWindowTask is a running task with an unsaved window _mon open and a
// saved window _git, in its repository, that is not.
func extraWindowTask(t *testing.T) (*harness, *restartingRuntime, string) {
	t.Helper()
	h := newHarness(t)
	tk := h.startedRootTask(t, "T")
	repo := tk.RepoNames()[0]
	tk.Windows = []task.Window{{Name: "git", Repo: repo, Command: []string{"lazygit"}}}
	if err := h.app.Store.Save(tk); err != nil {
		t.Fatal(err)
	}
	rt := &restartingRuntime{layoutRuntime: layoutRuntime{running: true}}
	rt.started = []runtime.Window{{Name: task.RootWindowName}, {Name: repo}, {Name: "_mon"}}
	h.app.Runtime = rt
	return h, rt, repo
}

func TestStatusShowsExtraWindows(t *testing.T) {
	h, _, _ := extraWindowTask(t)
	if err := h.exec(t, h.app.newStatusCmd(), "T"); err != nil {
		t.Fatal(err)
	}
	out := h.out.String()
	for _, want := range []string{"_git (saved)", "lazygit", "no-window", "_mon"} {
		if !strings.Contains(out, want) {
			t.Fatalf("status lacks %q:\n%s", want, out)
		}
	}

	h.out.Reset()
	if err := h.exec(t, h.app.newStatusCmd(), "T", "--json"); err != nil {
		t.Fatal(err)
	}
	var got []statusJSON
	if err := json.Unmarshal(h.out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	ws := got[0].Windows
	if len(ws) != 2 || ws[0].Name != "_git" || !ws[0].Saved || ws[0].Runtime != "no-window" ||
		ws[1].Name != "_mon" || ws[1].Saved || ws[1].Runtime != "running" {
		t.Fatalf("windows = %+v", ws)
	}
}

// A task with no extra windows still has the field, as an empty array.
func TestStatusJSONWindowsEmpty(t *testing.T) {
	h := newHarness(t)
	h.startedTask(t, "T")
	if err := h.exec(t, h.app.newStatusCmd(), "T", "--json"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.out.String(), `"windows": []`) {
		t.Fatalf("json:\n%s", h.out.String())
	}
}

func TestStopClosesOneExtraWindow(t *testing.T) {
	h, rt, repo := extraWindowTask(t)
	if err := h.exec(t, h.app.newStopCmd(), "T", "_mon"); err != nil {
		t.Fatal(err)
	}
	if _, open := rt.window("_mon"); open {
		t.Fatal("_mon still open")
	}
	for _, w := range []string{task.RootWindowName, repo} {
		if _, open := rt.window(w); !open {
			t.Fatalf("%s was closed too", w)
		}
	}
	if err := h.exec(t, h.app.newStopCmd(), "T", "_mon"); err == nil || !strings.Contains(err.Error(), "no open window _mon") {
		t.Fatalf("closing a closed window: %v", err)
	}
	// The saved window is not open, so there is nothing to close, and it
	// stays saved either way.
	if err := h.exec(t, h.app.newStopCmd(), "T", "_git"); err == nil {
		t.Fatal("closed a window that is not open")
	}
	if _, saved := h.load(t, "T").SavedWindow("git"); !saved {
		t.Fatal("saved window forgotten")
	}
}

func TestRestartExtraWindow(t *testing.T) {
	h, rt, repo := extraWindowTask(t)
	if err := h.exec(t, h.app.newRestartCmd(), "T", "_mon"); err == nil || !strings.Contains(err.Error(), "not saved") {
		t.Fatalf("restart of an unsaved window: %v", err)
	}
	rt.started = append(rt.started, runtime.Window{Name: "_git"})
	if err := h.exec(t, h.app.newRestartCmd(), "T", "_git"); err != nil {
		t.Fatal(err)
	}
	if len(rt.restarted) != 1 {
		t.Fatalf("restarted %+v", rt.restarted)
	}
	w := rt.restarted[0]
	if w.Name != "_git" || w.Dir != h.load(t, "T").Repos[repo].Worktree || len(w.Command) != 1 || w.Command[0] != "lazygit" {
		t.Fatalf("restarted %+v", w)
	}
	if err := h.exec(t, h.app.newRestartCmd(), "T", "_git", "--resume"); err == nil {
		t.Fatal("resumed an extra window")
	}
}

func TestDashExtraWindows(t *testing.T) {
	h, rt, _ := extraWindowTask(t)
	m := newDashModel(context.Background(), h.app, time.Second)
	m.expanded = map[string]bool{"T": true}
	m = drive(t, m, dashDataMsg(h.app.dashLoad(context.Background())))
	at := -1
	for i, r := range m.data.Rows {
		if r.Window == "_mon" {
			at = i
		}
	}
	if at < 0 {
		t.Fatalf("no _mon row: %+v", m.data.Rows)
	}
	if r := m.data.Rows[at]; r.label() != "T/_mon" || r.Runtime != status.RuntimeRunning || r.Saved {
		t.Fatalf("row %+v", r)
	}
	if !strings.Contains(m.View(), "_git (saved)") {
		t.Fatalf("view lacks the saved window:\n%s", m.View())
	}
	m.cursor = at
	m = drive(t, m, key("S"))
	if !strings.Contains(m.note, "S starts a primary window") {
		t.Fatalf("S on an extra window: %q", m.note)
	}
	m = drive(t, m, key("X"))
	if m.confirm == nil || !strings.Contains(m.confirm.prompt, "T/_mon") {
		t.Fatalf("confirm = %+v", m.confirm)
	}
	m = drive(t, m, key("y"))
	if m.noteErr {
		t.Fatal(m.note)
	}
	if _, open := rt.window("_mon"); open {
		t.Fatal("X left _mon open")
	}
}
