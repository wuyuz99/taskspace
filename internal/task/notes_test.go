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

func TestMigrateNotesMovesLegacyRecordsOnce(t *testing.T) {
	s := newTestStore(t)
	stamp := time.Date(2026, 9, 1, 10, 30, 0, 0, time.UTC)
	legacy := New("PAY-142")
	legacy.Title = "Expired cards"
	legacy.LegacyBrief = "Reject expired cards.\n"
	legacy.LegacyHandoff = "Frontend next.\n"
	legacy.LegacyHandoffUpdatedAt = &stamp
	if err := s.Save(legacy); err != nil {
		t.Fatal(err)
	}

	loaded, err := s.Load("PAY-142")
	if err != nil {
		t.Fatal(err)
	}
	moved, err := s.MigrateNotes(loaded)
	if err != nil {
		t.Fatal(err)
	}
	if !moved {
		t.Fatal("legacy record was not migrated")
	}

	brief, err := s.ReadNote("PAY-142", BriefFile)
	if err != nil {
		t.Fatal(err)
	}
	handoff, err := s.ReadNote("PAY-142", HandoffFile)
	if err != nil {
		t.Fatal(err)
	}
	if brief.Text != "Reject expired cards.\n" || handoff.Text != "Frontend next.\n" {
		t.Fatalf("notes = %q / %q", brief.Text, handoff.Text)
	}
	// The recorded handoff time carries over to the file it now lives in.
	if !handoff.UpdatedAt.Equal(stamp) {
		t.Errorf("UpdatedAt = %v, want %v", handoff.UpdatedAt, stamp)
	}

	// The inline copy is gone from disk, and the rest of the record survives.
	raw, err := os.ReadFile(filepath.Join(s.TasksDir(), "PAY-142.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"brief"`, `"handoff"`, `"handoff_updated_at"`} {
		if strings.Contains(string(raw), field) {
			t.Errorf("record still carries %s: %s", field, raw)
		}
	}
	again, err := s.Load("PAY-142")
	if err != nil {
		t.Fatal(err)
	}
	if again.Title != "Expired cards" {
		t.Errorf("migration lost the title: %+v", again)
	}
	if moved, err := s.MigrateNotes(again); err != nil || moved {
		t.Fatalf("second migration = %v, %v; want no-op", moved, err)
	}
}

// A note file is the live copy. If one exists, a stale inline value from an old
// record must not overwrite it.
func TestMigrateNotesDoesNotOverwriteAnExistingFile(t *testing.T) {
	s := newTestStore(t)
	if err := s.WriteNote("t", BriefFile, "current\n"); err != nil {
		t.Fatal(err)
	}
	legacy := New("t")
	legacy.LegacyBrief = "stale\n"
	if _, err := s.MigrateNotes(legacy); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadNote("t", BriefFile)
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "current\n" {
		t.Fatalf("file note = %q, want the file to win", got.Text)
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
