package cli

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/wuyuz99/taskspace/internal/task"
)

type closingRuntime struct {
	layoutRuntime
	closed []string
}

func (r *closingRuntime) CloseWindow(_ context.Context, _ string, window string) error {
	r.closed = append(r.closed, window)
	return nil
}

func TestPrimaryWindowStopAndRootStart(t *testing.T) {
	h := newHarness(t)
	tk := h.startedRootTask(t, "T")
	repo := tk.RepoNames()[0]
	rt := &closingRuntime{layoutRuntime: layoutRuntime{running: true}}
	h.app.Runtime = rt
	if err := h.app.Store.WriteSignal("T", repo, task.SignalWorking, ""); err != nil {
		t.Fatal(err)
	}
	if err := h.app.Store.WriteSignal("T", task.RootWindowName, task.SignalBlocked, ""); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newStopCmd(), "T", repo); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(rt.closed, []string{repo}) || rt.stopped {
		t.Fatalf("closed=%v stopped=%v", rt.closed, rt.stopped)
	}
	signals, err := h.app.Store.ReadSignals("T")
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := signals[repo]; exists {
		t.Fatal("stopped signal remains")
	}
	if _, exists := signals[task.RootWindowName]; !exists {
		t.Fatal("other signal removed")
	}
	if err := h.exec(t, h.app.newStopCmd(), "T", repo, "--root"); err == nil {
		t.Fatal("ambiguous window accepted")
	}
	if err := h.exec(t, h.app.newStartCmd(), "T", "--root"); err != nil {
		t.Fatal(err)
	}
	if len(rt.started) != 1 || rt.started[0].Name != task.RootWindowName {
		t.Fatalf("started=%+v", rt.started)
	}
}

// dash's X stops only the selected window, after confirming.
func TestDashStopsOneWindow(t *testing.T) {
	h, m := loadedDash(t)
	rt := &closingRuntime{layoutRuntime: layoutRuntime{running: true}}
	h.app.Runtime = rt
	row := m.data.Rows[m.cursor]
	m = drive(t, m, key("X"))
	if m.confirm == nil || !strings.Contains(m.confirm.prompt, "stop only "+row.label()) {
		t.Fatalf("confirm = %+v", m.confirm)
	}
	m = drive(t, m, key("y"))
	if m.noteErr || !slices.Equal(rt.closed, []string{row.Window}) || rt.stopped {
		t.Fatalf("note=%q closed=%v stopped=%v", m.note, rt.closed, rt.stopped)
	}
}
