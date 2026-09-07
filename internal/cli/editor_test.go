package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aw-tool/aw/internal/task"
)

// The editor opens the note file itself, so a rewrite of the task record while
// the editor is open cannot touch the note, and the note cannot clobber the
// record. There is nothing to reconcile.
func TestEditIsIndependentOfTheTaskRecord(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix editor command")
	}
	h := newHarness(t)
	if _, err := h.app.Store.Create("t"); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(t.TempDir(), "editor")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf 'My draft\\n' > \"$1\"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VISUAL", "'"+script+"'")

	// Another writer rewrites the whole record while the note is being edited.
	other, err := h.app.Store.Load("t")
	if err != nil {
		t.Fatal(err)
	}
	other.Title = "Updated elsewhere"
	if err := h.app.Store.Save(other); err != nil {
		t.Fatal(err)
	}

	if err := h.exec(t, h.app.newTaskCmd(), "brief", "t", "--edit"); err != nil {
		t.Fatal(err)
	}
	note, err := h.app.Store.ReadNote("t", task.BriefFile)
	if err != nil {
		t.Fatal(err)
	}
	latest, err := h.app.Store.Load("t")
	if err != nil {
		t.Fatal(err)
	}
	if note.Text != "My draft\n" || latest.Title != "Updated elsewhere" {
		t.Fatalf("note %q, title %q; both should have survived", note.Text, latest.Title)
	}
}

func TestEditNotes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix editor command")
	}
	h := newHarness(t)
	if _, err := h.app.Store.Create("PAY-142"); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(t.TempDir(), "editor with spaces")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '# Updated\\nNext step\\n' > \"$1\"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VISUAL", "'"+script+"'")
	t.Setenv("EDITOR", "false") // VISUAL takes precedence.
	for _, kind := range []string{"brief", "handoff"} {
		if err := h.exec(t, h.app.newTaskCmd(), kind, "PAY-142", "--edit"); err != nil {
			t.Fatal(err)
		}
	}
	brief, err := h.app.Store.ReadNote("PAY-142", task.BriefFile)
	if err != nil {
		t.Fatal(err)
	}
	handoff, err := h.app.Store.ReadNote("PAY-142", task.HandoffFile)
	if err != nil {
		t.Fatal(err)
	}
	if brief.Text != "# Updated\nNext step\n" || handoff.Text != brief.Text || handoff.UpdatedAt.IsZero() {
		t.Fatalf("edited notes: %q / %q", brief.Text, handoff.Text)
	}

	// An editor that changes nothing leaves the note, and its time, alone.
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "true")
	h.out.Reset()
	if err := h.exec(t, h.app.newTaskCmd(), "handoff", "PAY-142", "--edit"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.out.String(), "unchanged") {
		t.Errorf("no-op edit reported %q", h.out.String())
	}
	after, err := h.app.Store.ReadNote("PAY-142", task.HandoffFile)
	if err != nil {
		t.Fatal(err)
	}
	if !after.UpdatedAt.Equal(handoff.UpdatedAt) {
		t.Error("unchanged note changed timestamp")
	}

	// A failed editor leaves the note exactly as it was, and says where it is.
	t.Setenv("EDITOR", "false")
	err = h.exec(t, h.app.newTaskCmd(), "brief", "PAY-142", "--edit")
	if err == nil || !strings.Contains(err.Error(), h.app.Store.NotePath("PAY-142", task.BriefFile)) {
		t.Fatalf("failed editor: %v", err)
	}
	kept, err := h.app.Store.ReadNote("PAY-142", task.BriefFile)
	if err != nil || kept.Text != brief.Text {
		t.Fatalf("failed edit disturbed the note: %q", kept.Text)
	}
}

// Emptying a note in the editor means the same as --clear.
func TestEditEmptyingANoteClearsIt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix editor command")
	}
	h := newHarness(t)
	if _, err := h.app.Store.Create("t"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newTaskCmd(), "handoff", "t", "Backend done"); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(t.TempDir(), "editor")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n: > \"$1\"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VISUAL", "'"+script+"'")
	if err := h.exec(t, h.app.newTaskCmd(), "handoff", "t", "--edit"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(h.app.Store.NotePath("t", task.HandoffFile)); !os.IsNotExist(err) {
		t.Fatalf("emptied note is still on disk: %v", err)
	}
	note, err := h.app.Store.ReadNote("t", task.HandoffFile)
	if err != nil {
		t.Fatal(err)
	}
	if !note.UpdatedAt.IsZero() {
		t.Errorf("emptied note kept a timestamp: %v", note.UpdatedAt)
	}
}

func TestEditRejectsOtherInputs(t *testing.T) {
	h := newHarness(t)
	if _, err := h.app.Store.Create("t"); err != nil {
		t.Fatal(err)
	}
	for _, extra := range [][]string{{"text"}, {"--clear"}, {"--file", "missing"}} {
		args := append([]string{"brief", "t", "--edit"}, extra...)
		if err := h.exec(t, h.app.newTaskCmd(), args...); err == nil || !strings.Contains(err.Error(), "choose only one") {
			t.Fatalf("conflict accepted: %v", err)
		}
	}
}
