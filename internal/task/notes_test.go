package task

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The point of putting notes in .context is that a program running in any of
// the task's worktrees can read them with a relative path.
func TestNotesAreReachableFromAWorktree(t *testing.T) {
	s := newTestStore(t)
	if err := s.WriteNote("checkout", BriefFile, "Reject expired cards.\n"); err != nil {
		t.Fatal(err)
	}
	worktree := s.WorktreePath("checkout", "backend")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	fromWorktree := filepath.Join(worktree, "..", ".context", BriefFile)
	b, err := os.ReadFile(fromWorktree)
	if err != nil {
		t.Fatalf("../.context/%s is not readable from a worktree: %v", BriefFile, err)
	}
	if string(b) != "Reject expired cards.\n" {
		t.Fatalf("note content = %q", b)
	}
}

func TestNoteRoundTripClearAndTimestamp(t *testing.T) {
	s := newTestStore(t)

	empty, err := s.ReadNote("t", HandoffFile)
	if err != nil {
		t.Fatalf("reading a note that was never written: %v", err)
	}
	if empty.Text != "" || !empty.UpdatedAt.IsZero() {
		t.Fatalf("absent note = %+v, want zero", empty)
	}

	before := time.Now().Add(-time.Second)
	if err := s.WriteNote("t", HandoffFile, "Backend done.\n"); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadNote("t", HandoffFile)
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "Backend done.\n" {
		t.Errorf("Text = %q", got.Text)
	}
	// The timestamp is the file's own, so it cannot disagree with the text.
	if got.UpdatedAt.Before(before) {
		t.Errorf("UpdatedAt = %v, want no earlier than %v", got.UpdatedAt, before)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(s.NotePath("t", HandoffFile))
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("note mode = %o, want 600", perm)
		}
	}

	// Clearing removes the file, so nothing is left to carry a timestamp.
	if err := s.WriteNote("t", HandoffFile, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.NotePath("t", HandoffFile)); !os.IsNotExist(err) {
		t.Fatalf("cleared note still on disk: %v", err)
	}
	cleared, err := s.ReadNote("t", HandoffFile)
	if err != nil {
		t.Fatal(err)
	}
	if !cleared.UpdatedAt.IsZero() {
		t.Errorf("cleared note kept a timestamp: %v", cleared.UpdatedAt)
	}
	// Writing leaves no temp files behind.
	entries, err := os.ReadDir(s.ContextDir("t"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("context dir = %v, want empty", entries)
	}
}

func TestRemoveNotesReportsAndTidies(t *testing.T) {
	s := newTestStore(t)
	if err := s.WriteNote("t", BriefFile, "brief\n"); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteNote("t", HandoffFile, "handoff\n"); err != nil {
		t.Fatal(err)
	}
	held, err := s.RemoveNotes("t")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(held, ",") != HandoffFile+","+BriefFile { // directory order
		t.Errorf("held = %v", held)
	}
	// The workspace directory goes with the last thing in it.
	if _, err := os.Stat(s.TaskDir("t")); !os.IsNotExist(err) {
		t.Errorf("task directory survived: %v", err)
	}
	if held, err := s.RemoveNotes("t"); err != nil || held != nil {
		t.Fatalf("removing absent notes = %v, %v", held, err)
	}
}

// A task with worktrees still on disk keeps its directory when its notes go.
func TestRemoveNotesLeavesWorktreesAlone(t *testing.T) {
	s := newTestStore(t)
	if err := s.WriteNote("t", BriefFile, "brief\n"); err != nil {
		t.Fatal(err)
	}
	worktree := s.WorktreePath("t", "backend")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RemoveNotes("t"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(worktree); err != nil {
		t.Fatalf("worktree was removed with the notes: %v", err)
	}
}

func TestNoteFailuresAreReported(t *testing.T) {
	s := newTestStore(t)
	if err := s.WriteNote("../escape", BriefFile, "x"); err == nil {
		t.Error("WriteNote accepted an unsafe task name")
	}
	if _, err := s.RemoveNotes("../escape"); err == nil {
		t.Error("RemoveNotes accepted an unsafe task name")
	}

	// A file where .context should be.
	if err := os.MkdirAll(s.TaskDir("t"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.ContextDir("t"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteNote("t", BriefFile, "x"); err == nil {
		t.Error("WriteNote wrote under a file")
	}
	if err := s.Save(&Task{Name: "t", LegacyBrief: "old"}); err == nil {
		t.Error("upgrading a legacy record hid a note failure")
	}

	// A non-empty directory where the note should be can be neither replaced
	// nor cleared.
	s = newTestStore(t)
	if err := os.MkdirAll(filepath.Join(s.NotePath("t", BriefFile), "inner"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteNote("t", BriefFile, "x"); err == nil {
		t.Error("WriteNote replaced a directory")
	}
	if err := s.WriteNote("t", BriefFile, ""); err == nil {
		t.Error("WriteNote cleared a directory")
	}
	if _, err := s.ReadNote("t", BriefFile); err == nil {
		t.Error("ReadNote read a directory")
	}
}
