package cli

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// listed are the choices a form's view lists, one per line, in order.
func listed(view string) []string {
	var out []string
	for _, l := range strings.Split(view, "\n") {
		l = strings.TrimSpace(l)
		if c, ok := strings.CutPrefix(l, "[ ] "); ok {
			out = append(out, c)
		} else if c, ok := strings.CutPrefix(l, "[x] "); ok {
			out = append(out, c)
		}
	}
	return out
}

// ↑ and ↓ move along a pick's list, typing filters it, and a choice the
// filter hides stays picked.
func TestDashPickMovesAndFilters(t *testing.T) {
	f := &dashForm{fields: []dashField{
		dashField{label: "repositories"}.pick([]string{"api", "payments-service", "web", "pay-web"}, false),
		{label: "name"},
	}}
	down := tea.KeyMsg{Type: tea.KeyDown}
	f.key(down)
	f.key(key(" "))
	if got := f.field("repositories").text(); got != "payments-service" || f.focus != 0 {
		t.Fatalf("picked %q, focus %d", got, f.focus)
	}
	for _, r := range "WEB" {
		f.key(key(string(r)))
	}
	if got := strings.Join(listed(f.view(24)), " "); got != "web pay-web" {
		t.Fatalf("filtered to %q", got)
	}
	if !strings.Contains(f.view(24), "payments-service") {
		t.Fatal("a picked choice the filter hides is not shown as picked")
	}
	f.key(down)
	f.key(down) // stops at the last
	f.key(key(" "))
	if got := f.field("repositories").text(); got != "payments-service pay-web" {
		t.Fatalf("picked %q", got)
	}
	f.key(tea.KeyMsg{Type: tea.KeyCtrlU})
	if got := len(listed(f.view(24))); got != 4 {
		t.Fatalf("ctrl+u left %d choices", got)
	}
	if f.field("repositories").at != 3 {
		t.Fatal("clearing the filter moved the cursor")
	}
	// Up from the first choice stays in the list; tab leaves it.
	for range 5 {
		f.key(tea.KeyMsg{Type: tea.KeyUp})
	}
	if f.focus != 0 || f.field("repositories").at != 0 {
		t.Fatalf("focus %d at %d", f.focus, f.field("repositories").at)
	}
	f.key(tab)
	if f.focus != 1 || len(listed(f.view(24))) != 0 {
		t.Fatal("an unfocused pick still lists its choices")
	}
}

// A list longer than the screen scrolls with the cursor, saying which part
// it shows, and nothing wraps, in names of wide characters too.
func TestDashPickScrolls(t *testing.T) {
	for _, stem := range []string{"repo", "仓库"} {
		t.Run(stem, func(t *testing.T) {
			var names []string
			for i := range 30 {
				names = append(names, fmt.Sprintf("%s-%02d", stem, i))
			}
			f := &dashForm{title: "new", fields: []dashField{{label: "name"}, dashField{label: "repositories"}.pick(names, false)}}
			f.key(tab)
			const rows = 12
			check := func(first, last string) {
				t.Helper()
				v := f.view(rows)
				if n := strings.Count(v, "\n") + 1; n > rows {
					t.Fatalf("%d lines in %d:\n%s", n, rows, v)
				}
				got := listed(v)
				if got[0] != first || got[len(got)-1] != last {
					t.Fatalf("shows %s to %s:\n%s", got[0], got[len(got)-1], v)
				}
			}
			check(stem+"-00", stem+"-06")
			for range 20 {
				f.key(tea.KeyMsg{Type: tea.KeyDown})
			}
			check(stem+"-17", stem+"-23")
			if !strings.Contains(f.view(rows), "18-24 of 30") {
				t.Fatalf("no position:\n%s", f.view(rows))
			}
			for range 20 {
				f.key(tea.KeyMsg{Type: tea.KeyDown})
			}
			check(stem+"-23", stem+"-29")
		})
	}
}

// n's form takes the whole screen of a short terminal, without the table,
// and esc brings the table back.
func TestDashCreateFormTakesTheScreen(t *testing.T) {
	h := newHarness(t)
	h.startedTask(t, "A-1")
	c, err := h.app.Store.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	c.Repos = map[string]string{}
	for i := range 25 {
		c.Repos[fmt.Sprintf("service-%02d", i)] = t.TempDir()
	}
	if err := h.app.Store.SaveConfig(c); err != nil {
		t.Fatal(err)
	}
	m := newDashModel(t.Context(), h.app, time.Second)
	m = drive(t, m, dashDataMsg(h.app.dashLoad(t.Context())))
	m = drive(t, m, tea.WindowSizeMsg{Width: 50, Height: 14})
	m = drive(t, m, key("n"))
	m = drive(t, m, tab)
	v := m.View()
	lines := strings.Split(v, "\n")
	if len(lines) > 14 || strings.Contains(v, "A-1") {
		t.Fatalf("%d lines, table shown:\n%s", len(lines), v)
	}
	for _, l := range lines {
		if w := lipgloss.Width(l); w > 50 {
			t.Fatalf("%d columns: %q", w, l)
		}
	}
	if got := listed(v); len(got) < 3 || got[0] != "service-00" {
		t.Fatalf("lists %v:\n%s", got, v)
	}
	m = drive(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.form != nil || !strings.Contains(m.View(), "A-1") {
		t.Fatalf("esc:\n%s", m.View())
	}
}
