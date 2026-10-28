package cli

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
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
	if m.form == nil || !strings.Contains(m.View(), "registered: backend") {
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
	m = drive(t, m, key("backend"))
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
	for range 3 {
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
