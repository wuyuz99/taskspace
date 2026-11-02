package cli

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// The common keys stay on screen whatever the width, on one line at 80
// columns, and the table keeps its height.
func TestDashLegendIsNotCutOff(t *testing.T) {
	_, m := loadedDash(t)
	if got := dashLegend(80); len(got) != 1 {
		t.Fatalf("legend at 80 columns: %q", got)
	}
	for _, size := range []struct{ w, h int }{{80, 24}, {30, 24}, {120, 30}, {240, 50}} {
		m = drive(t, m, tea.WindowSizeMsg{Width: size.w, Height: size.h})
		view := m.View()
		for _, k := range dashKeys {
			if k.common && !strings.Contains(view, k.label) {
				t.Errorf("%dx%d: %q is missing:\n%s", size.w, size.h, k.label, view)
			}
		}
		if n := strings.Count(view, "\n") + 1; n > size.h {
			t.Errorf("%dx%d: %d lines:\n%s", size.w, size.h, n, view)
		}
	}
}

// ? shows every key under its group, fits the screen, and closes on any key
// without that key doing anything.
func TestDashKeyPanel(t *testing.T) {
	h, m := loadedDash(t)
	for _, size := range []struct{ w, h int }{{80, 24}, {40, 24}, {120, 30}} {
		m = drive(t, m, tea.WindowSizeMsg{Width: size.w, Height: size.h})
		open := drive(t, m, key("?"))
		view := open.View()
		for _, k := range dashKeys {
			if !strings.Contains(view, k.label) {
				t.Errorf("%dx%d: %q is missing:\n%s", size.w, size.h, k.label, view)
			}
		}
		for _, group := range dashGroups {
			if !strings.Contains(view, group) {
				t.Errorf("%dx%d: group %s is missing", size.w, size.h, group)
			}
		}
		if n := strings.Count(view, "\n") + 1; n > size.h {
			t.Errorf("%dx%d: %d lines:\n%s", size.w, size.h, n, view)
		}
		next, cmd := open.Update(key("R"))
		closed := next.(dashModel)
		if closed.keys || closed.confirm != nil || cmd != nil {
			t.Fatalf("R did more than close the panel: %+v", closed)
		}
	}
	if !h.app.Store.Exists("A-1") {
		t.Fatal("task deleted")
	}
}

func TestDashKeysHaveAGroup(t *testing.T) {
	for _, k := range dashKeys {
		if !slices.Contains(dashGroups, k.group) || k.label == "" {
			t.Errorf("%q: group %q, label %q", k.keys, k.group, k.label)
		}
	}
}

func TestDashHelpNamesEveryKey(t *testing.T) {
	long := (&App{}).newDashCmd().Long
	for _, k := range dashKeys {
		first, _, _ := strings.Cut(k.help, "\n")
		if !strings.Contains(long, k.keys) || !strings.Contains(long, first) {
			t.Errorf("--help leaves out %q", k.keys)
		}
	}
}

// A key the table lists does something: on a row it applies to, the screen,
// the model or the returned command differs from pressing nothing.
func TestDashKeysAreHandled(t *testing.T) {
	_, m := loadedDash(t)
	for _, k := range dashKeys {
		press := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k.press)}
		switch k.press {
		case "enter":
			press = tea.KeyMsg{Type: tea.KeyEnter}
		case " ":
			press = tea.KeyMsg{Type: tea.KeySpace, Runes: []rune(" ")}
		}
		next, cmd := m.Update(press)
		if cmd == nil && reflect.DeepEqual(next.(dashModel), m) {
			t.Errorf("%q does nothing", k.press)
		}
	}
}
