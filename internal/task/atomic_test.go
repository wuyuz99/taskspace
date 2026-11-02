package task

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteFileAtomicCreatesDirectoryAndLeavesNoTemp(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a", "b")
	path := filepath.Join(dir, "f.json")
	if err := WriteFileAtomic(path, []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(path, []byte("two"), 0o600); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(path); err != nil || string(b) != "two" {
		t.Fatalf("read %q, %v", b, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("left behind: %v", entries)
	}
}

// A hard link keeps the old file's contents only if the path was replaced,
// not written in place, which is what surviving a crash during the write
// depends on.
func TestRunDataIsReplacedNotRewritten(t *testing.T) {
	s := NewStore(t.TempDir())
	if err := s.MarkRunning("t", Running{Root: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.RememberWindow("t", Window{Name: "w", Command: []string{"old"}}); err != nil {
		t.Fatal(err)
	}
	type file struct{ path, link string }
	files := []file{
		{s.runningPath("t"), filepath.Join(t.TempDir(), "running")},
		{s.openedWindowPath("t", "w"), filepath.Join(t.TempDir(), "window")},
	}
	before := make([]string, len(files))
	for i, f := range files {
		if err := os.Link(f.path, f.link); err != nil {
			t.Skipf("no hard links here: %v", err)
		}
		b, err := os.ReadFile(f.link)
		if err != nil {
			t.Fatal(err)
		}
		before[i] = string(b)
	}
	if err := s.MarkRunning("t", Running{Repos: []string{"api"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.RememberWindow("t", Window{Name: "w", Command: []string{"new"}}); err != nil {
		t.Fatal(err)
	}
	for i, f := range files {
		b, err := os.ReadFile(f.link)
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != before[i] {
			t.Errorf("%s was rewritten in place: %q", f.path, b)
		}
	}
	if r, ok := s.Running("t"); !ok || len(r.Repos) != 1 {
		t.Fatalf("running = %+v, %v", r, ok)
	}
}
