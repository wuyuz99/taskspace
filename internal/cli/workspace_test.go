package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/wuyuz99/taskspace/internal/task"
)

func TestRootPathBeforeStart(t *testing.T) {
	h := newHarness(t)
	tk, err := h.app.Store.Create("PAY-142")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"backend", "frontend"} {
		tk.SetRepo(name, task.Repo{})
	}
	if err := h.app.Store.Save(tk); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newPathCmd(), tk.Name, "--root"); err != nil {
		t.Fatal(err)
	}
	if h.out.String() != h.app.Store.TaskDir(tk.Name)+"\n" {
		t.Fatalf("path: %q", h.out.String())
	}
	if _, err := os.Stat(h.app.Store.TaskDir(tk.Name)); !os.IsNotExist(err) {
		t.Fatal("path created directory")
	}
	if err := h.exec(t, h.app.newPathCmd(), tk.Name, "backend", "--root"); err == nil {
		t.Fatal("root and repo accepted")
	}
	if err := h.exec(t, h.app.newShellCmd(), tk.Name, "backend", "--root"); err == nil {
		t.Fatal("root and repo accepted")
	}
}

func TestRootShellEnvironment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix shell test")
	}
	h := newHarness(t)
	if _, err := h.app.Store.Create("PAY-142"); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(t.TempDir(), "probe")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s\\n' \"$PWD\" \"$TSPACE_TASK_DIR\" \"$TSPACE_TASK\" \"$TSPACE_REPO\" \"$TSPACE_WORKTREE\" > probe.txt\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", script)
	if err := h.exec(t, h.app.newShellCmd(), "PAY-142", "--root"); err != nil {
		t.Fatal(err)
	}
	dir := h.app.Store.TaskDir("PAY-142")
	b, err := os.ReadFile(filepath.Join(dir, "probe.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != strings.Join([]string{dir, dir, "PAY-142", "", dir, ""}, "\n") {
		t.Fatalf("environment: %q", b)
	}
}
