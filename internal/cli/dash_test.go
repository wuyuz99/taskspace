package cli

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/wuyuz99/taskspace/internal/runtime"
	"github.com/wuyuz99/taskspace/internal/status"
	"github.com/wuyuz99/taskspace/internal/task"
)

// loadedDash returns a dashboard over two stopped tasks, as it looks after its
// first refresh.
func loadedDash(t *testing.T) (*harness, dashModel) {
	t.Helper()
	h := newHarness(t)
	h.startedTask(t, "A-1")
	h.startedTask(t, "B-2")
	m := newDashModel(context.Background(), h.app, time.Second)
	if m.Init() == nil {
		t.Fatal("Init schedules no refresh")
	}
	m = drive(t, m, dashDataMsg(h.app.dashLoad(context.Background())))
	if !m.loaded || len(m.data.Rows) < 2 {
		t.Fatalf("rows: %+v", m.data.Rows)
	}
	return h, m
}

func TestDashNavigation(t *testing.T) {
	_, m := loadedDash(t)
	last := len(m.data.Rows) - 1

	for _, step := range []struct {
		key  tea.KeyMsg
		want int
	}{
		{key("k"), 0}, // already at the top
		{key("j"), 1},
		{tea.KeyMsg{Type: tea.KeyUp}, 0},
		{key("G"), last},
		{key("down"), last}, // already at the bottom
		{tea.KeyMsg{Type: tea.KeyHome}, 0},
		{tea.KeyMsg{Type: tea.KeyEnd}, last},
		{key("g"), 0},
	} {
		m = drive(t, m, step.key)
		if m.cursor != step.want {
			t.Fatalf("after %q cursor = %d, want %d", step.key, m.cursor, step.want)
		}
	}

	for _, quit := range []tea.KeyMsg{key("q"), {Type: tea.KeyEsc}, {Type: tea.KeyCtrlC}} {
		if _, cmd := m.Update(quit); cmd == nil {
			t.Errorf("%q does not quit", quit)
		} else if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Errorf("%q does not quit", quit)
		}
	}
	// Unbound keys do nothing.
	if next, cmd := m.Update(key("z")); cmd != nil || next.(dashModel).cursor != 0 {
		t.Error("unbound key did something")
	}
}

func TestDashCursorFollowsItsRow(t *testing.T) {
	h, m := loadedDash(t)
	m = drive(t, m, key("G"))
	want := m.data.Rows[m.cursor]

	// A task sorting before the selected one appears; the cursor stays put on
	// the same row rather than the same position.
	h.startedTask(t, "0-first")
	m = drive(t, m, dashDataMsg(h.app.dashLoad(context.Background())))
	if got := m.data.Rows[m.cursor]; got.Task != want.Task || got.Window != want.Window {
		t.Fatalf("cursor on %s/%s, want %s/%s", got.Task, got.Window, want.Task, want.Window)
	}

	// When everything goes, the cursor clamps to zero and actions are no-ops.
	m = drive(t, m, dashDataMsg(dashData{}))
	if m.cursor != 0 {
		t.Fatalf("cursor = %d on an empty list", m.cursor)
	}
	if next, cmd := m.Update(key("s")); cmd != nil || next.(dashModel).note != "" {
		t.Fatal("action on an empty list did something")
	}
}

func TestDashTickDoesNotPileUpRefreshes(t *testing.T) {
	_, m := loadedDash(t)
	next, cmd := m.Update(dashTickMsg{})
	m = next.(dashModel)
	if !m.loading || cmd == nil {
		t.Fatal("tick did not start a refresh")
	}
	// A second tick while loading only reschedules itself.
	next, cmd = m.Update(dashTickMsg{})
	if !next.(dashModel).loading || cmd == nil {
		t.Fatal("tick while loading lost its schedule")
	}
	m = drive(t, m, dashDataMsg(m.data))
	if m.loading {
		t.Fatal("data did not end the refresh")
	}
}

func TestDashActionsOnAStoppedTask(t *testing.T) {
	h, m := loadedDash(t)
	row := m.data.Rows[0]
	if row.Runtime != status.RuntimeNoSession {
		t.Fatalf("row runtime = %s, want no-session", row.Runtime)
	}

	m = drive(t, m, key("enter"))
	if !strings.Contains(m.note, "has no window to attach to") || !strings.Contains(m.note, "press s") {
		t.Fatalf("enter on a stopped task: %q", m.note)
	}
	m = drive(t, m, key("r"))
	if !strings.Contains(m.note, "only a dead window can be restarted") {
		t.Fatalf("restart of a stopped window: %q", m.note)
	}
	m = drive(t, m, key("c"))
	if !strings.Contains(m.note, "has no signal") {
		t.Fatalf("clear without a signal: %q", m.note)
	}

	// s runs the same start a person would type; the stub runtime accepts it.
	next, cmd := m.Update(key("s"))
	if m = next.(dashModel); m.note != "starting "+row.Task+"..." || cmd == nil {
		t.Fatalf("start: %q", m.note)
	}
	done, ok := cmd().(dashDoneMsg)
	if !ok || done.err != nil {
		t.Fatalf("start result: %+v", done)
	}

	// Confirming a stop runs it.
	rt := &windowRuntime{running: true, panes: map[string]runtime.PaneState{}}
	h.app.Runtime = &runningRuntime{rt}
	m = drive(t, m, key("x"))
	if m.confirm == nil {
		t.Fatal("stop did not ask")
	}
	m = drive(t, m, key("y"))
	if !rt.stopped || m.confirm != nil || m.noteErr || !strings.Contains(m.note, "stopped "+row.Task) {
		t.Fatalf("confirmed stop: stopped=%v note=%q", rt.stopped, m.note)
	}
}

// A failure from the runtime shows on the status line rather than vanishing.
func TestDashShowsActionErrors(t *testing.T) {
	h, m := loadedDash(t)
	h.app.Runtime = &failingFocus{&windowRuntime{running: true, panes: map[string]runtime.PaneState{}}}
	m.data.Rows[0].Runtime = status.RuntimeRunning
	m = drive(t, m, key("enter"))
	if !m.noteErr || m.note != "focus broke" {
		t.Fatalf("focus error: noteErr=%v note=%q", m.noteErr, m.note)
	}
	// The next successful action clears the error flag.
	m = drive(t, m, dashDoneMsg{text: "fine"})
	if m.noteErr || m.note != "fine" {
		t.Fatalf("after success: noteErr=%v note=%q", m.noteErr, m.note)
	}
}

type failingFocus struct{ *windowRuntime }

func (f *failingFocus) Focus(context.Context, string, string) (bool, error) {
	return false, errors.New("focus broke")
}

func TestDashStyles(t *testing.T) {
	// Each state gets its own style, so the eye finds blocked and dead rows.
	for _, s := range []status.RuntimeState{status.RuntimeRunning, status.RuntimeDead, status.RuntimeUnknown, status.RuntimeNoSession} {
		_ = runtimeStyle(s).Render(string(s))
	}
	if signalStyle(nil).GetBold() {
		t.Error("no signal is bold")
	}
	for st, bold := range map[task.SignalState]bool{
		task.SignalBlocked: true, task.SignalFailed: true, task.SignalDone: false, task.SignalWorking: false,
	} {
		if got := signalStyle(&task.Signal{State: st}).GetBold(); got != bold {
			t.Errorf("%s bold = %v, want %v", st, got, bold)
		}
	}
	if runtimeStyle(status.RuntimeDead).GetForeground() == runtimeStyle(status.RuntimeRunning).GetForeground() {
		t.Error("dead and running look the same")
	}
}
