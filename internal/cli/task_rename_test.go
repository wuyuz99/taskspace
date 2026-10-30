package cli

import (
	"strings"
	"testing"
	"time"
)

func TestTaskRenameCommand(t *testing.T) {
	h := newHarness(t)
	tk := h.startedTask(t, "OLD")
	if err := h.exec(t, h.app.newTaskCmd(), "rename", "OLD", "NEW", "--branches"); err != nil {
		t.Fatal(err)
	}
	got := h.load(t, "NEW")
	if got.Repos[tk.RepoNames()[0]].Branch != "agent/NEW" || !strings.Contains(h.out.String(), "renamed OLD to NEW") {
		t.Fatalf("record %+v; output %s", got, h.out.String())
	}
	if h.app.Store.Exists("OLD") {
		t.Fatal("old task remains")
	}
}

func TestDashRenameTask(t *testing.T) {
	h := newHarness(t)
	h.startedTask(t, "OLD")
	m := newDashModel(t.Context(), h.app, time.Second)
	m = drive(t, m, dashDataMsg(h.app.dashLoad(t.Context())))
	m = drive(t, m, key("N"))
	if m.form == nil {
		t.Fatal("rename form did not open")
	}
	m = drive(t, m, key("OLD"))
	if !strings.Contains(m.form.note, "already exists") {
		t.Fatalf("form note: %s", m.form.note)
	}
	m.form.fields[0].value = []rune("NEW")
	m.form.fields[0].cursor = 3
	m = drive(t, m, key("enter"))
	if m.form != nil || m.noteErr || !strings.Contains(m.note, "renamed OLD to NEW") {
		t.Fatalf("rename note: %s", m.note)
	}
	m = drive(t, m, dashDataMsg(h.app.dashLoad(t.Context())))
	if row, ok := m.selected(); !ok || row.Task != "NEW" {
		t.Fatalf("selected %+v", row)
	}
	if !h.app.Store.Exists("NEW") || h.app.Store.Exists("OLD") {
		t.Fatal("dashboard did not rename")
	}
}
