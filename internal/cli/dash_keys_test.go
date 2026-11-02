package cli

import (
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// Every key's legend entry is on screen, whatever the width, and the table
// keeps its height.
func TestDashLegendIsNotCutOff(t *testing.T) {
	_, m := loadedDash(t)
	for _, size := range []struct{ w, h int }{{80, 24}, {60, 24}, {120, 30}, {240, 50}} {
		m = drive(t, m, tea.WindowSizeMsg{Width: size.w, Height: size.h})
		view := m.View()
		for _, k := range dashKeys {
			if k.legend != "" && !strings.Contains(view, k.legend) {
				t.Errorf("%dx%d: %q is missing:\n%s", size.w, size.h, k.legend, view)
			}
		}
		if n := strings.Count(view, "\n") + 1; n > size.h {
			t.Errorf("%dx%d: %d lines:\n%s", size.w, size.h, n, view)
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
