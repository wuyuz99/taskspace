package task

import (
	"slices"
	"testing"
)

func TestRememberedWindows(t *testing.T) {
	s := NewStore(t.TempDir())
	w := Window{Name: "git", Repo: "r", Command: []string{"lazygit"}}
	if err := s.RememberWindow("t", w); err != nil {
		t.Fatal(err)
	}
	got, ok := s.OpenedWindow("t", "_git")
	if !ok || got.Repo != "r" || !slices.Equal(got.Command, w.Command) {
		t.Fatalf("got %+v, %v", got, ok)
	}
	if _, ok := s.OpenedWindow("t", "_../../x"); ok {
		t.Fatal("a path-like name was read")
	}

	// A rename onto what the disk may take as the same name keeps the record.
	if err := s.RenameWindow("t", "git", "Git"); err != nil {
		t.Fatal(err)
	}
	if got, ok := s.OpenedWindow("t", "Git"); !ok || got.Name != "Git" {
		t.Fatalf("after rename %+v, %v", got, ok)
	}
	// Renaming an unknown window clears what an earlier one left at the name.
	if err := s.RenameWindow("t", "nope", "Git"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.OpenedWindow("t", "Git"); ok {
		t.Fatal("a window inherited a record")
	}

	if err := s.RememberWindow("t", w); err != nil {
		t.Fatal(err)
	}
	if err := s.ForgetWindows("t"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.OpenedWindow("t", "git"); ok {
		t.Fatal("still remembered")
	}
}
