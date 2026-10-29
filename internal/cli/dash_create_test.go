package cli

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var tab = tea.KeyMsg{Type: tea.KeyTab}

func TestDashCreateTask(t *testing.T) {
	h := newHarness(t)
	h.startedTask(t, "A-1")
	if err := h.exec(t, h.app.newConfigCmd(), "repo", "backend", h.repo); err != nil {
		t.Fatal(err)
	}
	m := newDashModel(t.Context(), h.app, time.Second)
	m = drive(t, m, dashDataMsg(h.app.dashLoad(t.Context())))

	m = drive(t, m, key("n"))
	if m.form == nil || !strings.Contains(m.View(), "[ ] backend") {
		t.Fatalf("form:\n%s", m.View())
	}
	// Keys go to the form, not to dash: q types a q.
	m = drive(t, m, key("q"))
	if m.form == nil || m.form.field("name").text() != "q" {
		t.Fatal("q left the form")
	}
	m = drive(t, m, tea.KeyMsg{Type: tea.KeyBackspace})
	m = drive(t, m, key("A-1"))
	if !strings.Contains(m.form.note, "already exists") {
		t.Fatalf("taken name: %q", m.form.note)
	}
	m = drive(t, m, key("enter"))
	if m.form == nil || h.app.Store.Exists("A-1x") {
		t.Fatal("submitted a taken name")
	}
	for range 3 {
		m = drive(t, m, tea.KeyMsg{Type: tea.KeyBackspace})
	}
	m = drive(t, m, key("-bad"))
	if !strings.Contains(m.form.note, "must start with a letter") {
		t.Fatalf("bad name: %q", m.form.note)
	}
	m.form.fields[0].value, m.form.fields[0].cursor = []rune("NEW-1"), 5
	m = drive(t, m, tab)
	if !strings.Contains(m.form.check(m.form), "needs a repository") {
		t.Fatal("start without a repository allowed")
	}
	// Letters do not type into a pick; space picks the choice at the cursor.
	m = drive(t, m, key("q"))
	m = drive(t, m, key(" "))
	if got := m.form.field("repositories").text(); got != "backend" {
		t.Fatalf("picked %q", got)
	}
	m = drive(t, m, key("enter"))
	if m.form != nil || m.noteErr || !strings.Contains(m.note, "created task NEW-1") || !strings.Contains(m.note, "is up") {
		t.Fatalf("form=%v note=%q", m.form != nil, m.note)
	}
	tk := h.load(t, "NEW-1")
	if _, ok := tk.Repo("backend"); !ok {
		t.Fatalf("repos %v", tk.RepoNames())
	}
	m = drive(t, m, dashDataMsg(h.app.dashLoad(t.Context())))
	if r, ok := m.selected(); !ok || r.Task != "NEW-1" {
		t.Fatalf("cursor on %+v", r)
	}
}

func TestDashCreateCancelAndToggle(t *testing.T) {
	h := newHarness(t)
	m := newDashModel(t.Context(), h.app, time.Second)
	m = drive(t, m, dashDataMsg(h.app.dashLoad(t.Context())))
	if !strings.Contains(m.View(), "press n") {
		t.Fatalf("empty view:\n%s", m.View())
	}
	m = drive(t, m, key("n"))
	m = drive(t, m, key("T"))
	for range 4 {
		m = drive(t, m, tab)
	}
	m = drive(t, m, key(" "))
	if m.form.field("start").on {
		t.Fatal("space did not untick start")
	}
	m = drive(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.form != nil || m.note != "cancelled" || h.app.Store.Exists("T") {
		t.Fatalf("esc: form=%v note=%q", m.form != nil, m.note)
	}
	// Without start, a task needs no repository.
	m = drive(t, m, key("n"))
	m = drive(t, m, key("T"))
	m.form.field("start").on = false
	m = drive(t, m, key("enter"))
	if m.noteErr || !h.app.Store.Exists("T") {
		t.Fatalf("note %q", m.note)
	}
}

// Typed words are never read as flags, an unknown project is refused in
// the form, and a pasted line break becomes a blank.
func TestDashCreateInputIsData(t *testing.T) {
	h := newHarness(t)
	m := newDashModel(t.Context(), h.app, time.Second)
	m = drive(t, m, dashDataMsg(h.app.dashLoad(t.Context())))
	m = drive(t, m, key("n"))
	m = drive(t, m, key("T"))
	m = drive(t, m, tab)
	m = drive(t, m, tab)
	m = drive(t, m, key("a\nb"))
	if got := m.form.field("more").text(); got != "a b" {
		t.Fatalf("pasted %q", got)
	}
	// A project deleted after the form opened is still refused.
	project := m.form.field("project")
	project.choices, project.picked = []string{"nope"}, []bool{true}
	if m.form.check(m.form) == "" {
		t.Fatal("unknown project accepted")
	}
	project.picked[0] = false
	m.form.field("more").value = []rune("--help")
	m = drive(t, m, key("enter"))
	if !m.noteErr || h.app.Store.Exists("T") {
		t.Fatalf("--help: note=%q", m.note)
	}
}

// A saved project is picked, one at most, and a picked repository typed
// again with a branch is named once, as typed.
func TestDashCreatePicks(t *testing.T) {
	h := newHarness(t)
	for _, args := range [][]string{{"repo", "backend", h.repo}, {"repo", "infra", mkRepo(t)}} {
		if err := h.exec(t, h.app.newConfigCmd(), args...); err != nil {
			t.Fatal(err)
		}
	}
	m := newDashModel(t.Context(), h.app, time.Second)
	m = drive(t, m, dashDataMsg(h.app.dashLoad(t.Context())))
	m = drive(t, m, key("n"))
	f := m.form
	repos := f.field("repositories")
	if strings.Join(repos.choices, " ") != "backend infra" {
		t.Fatalf("choices %v", repos.choices)
	}
	repos.picked = []bool{true, true}
	f.field("more").value = []rune("backend@fix-1")
	if got := strings.Join(createRepos(f), " "); got != "infra backend@fix-1" {
		t.Fatalf("repos %q", got)
	}

	project := dashField{label: "project"}.pick([]string{"p", "q"}, true)
	f = &dashForm{fields: []dashField{project}, check: func(*dashForm) string { return "" }}
	f.key(key(" "))
	f.key(tea.KeyMsg{Type: tea.KeyRight})
	f.key(key(" "))
	if got := f.field("project").text(); got != "q" {
		t.Fatalf("project %q", got)
	}
	f.key(key(" "))
	if got := f.field("project").text(); got != "" {
		t.Fatalf("dropped project %q", got)
	}
}

// A pick longer than the screen scrolls to keep the choice at the cursor in
// view, marking the side where more are hidden.
func TestDashPickScrolls(t *testing.T) {
	var names []string
	for i := range 10 {
		names = append(names, fmt.Sprintf("repo-%d", i))
	}
	f := &dashForm{fields: []dashField{dashField{label: "repositories"}.pick(names, false)}}
	line := func() string {
		for _, l := range strings.Split(f.view(40), "\n") {
			if strings.Contains(l, "repositories") {
				if w := lipgloss.Width(l); w > 40 {
					t.Fatalf("%d columns: %q", w, l)
				}
				return l
			}
		}
		t.Fatal("no pick line")
		return ""
	}
	if l := line(); !strings.Contains(l, "repo-0") || strings.Contains(l, "‹") || !strings.HasSuffix(l, "›") {
		t.Fatalf("at the start: %q", l)
	}
	for range 9 {
		f.key(tea.KeyMsg{Type: tea.KeyRight})
	}
	if l := line(); !strings.Contains(l, "repo-9") || strings.Contains(l, "repo-0") || !strings.Contains(l, "‹") || strings.HasSuffix(l, "›") {
		t.Fatalf("at the end: %q", l)
	}
	f.key(tea.KeyMsg{Type: tea.KeyLeft})
	if l := line(); !strings.Contains(l, "repo-8") {
		t.Fatalf("one back: %q", l)
	}
}
