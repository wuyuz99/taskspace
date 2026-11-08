package fspath

import (
	"os"
	"path/filepath"
	"testing"
)

func writable(t *testing.T, path string) bool {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()&0o200 != 0
}

// Files go read-only and come back writable; git's own entry and the
// directories are left as they are.
func TestSetReadOnly(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "sub", "a.go")
	gitFile := filepath.Join(dir, ".git")
	nestedGit := filepath.Join(dir, "sub", ".git")
	for _, p := range []string{file, gitFile, nestedGit} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := SetReadOnly(dir, true); err != nil {
		t.Fatal(err)
	}
	if writable(t, file) || writable(t, nestedGit) {
		t.Fatal("a file stayed writable")
	}
	if !writable(t, gitFile) || !writable(t, filepath.Join(dir, "sub")) {
		t.Fatal("git's entry or a directory was made read-only")
	}
	if err := os.WriteFile(file, []byte("y"), 0o644); err == nil {
		t.Fatal("a read-only file was written")
	}
	if err := SetReadOnly(dir, false); err != nil {
		t.Fatal(err)
	}
	if !writable(t, file) {
		t.Fatal("the file stayed read-only")
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
}
