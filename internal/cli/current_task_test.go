package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCurrentTaskDirectory(t *testing.T) {
	h := newHarness(t)
	h.startedTask(t, "HERE")
	dir := filepath.Join(h.app.Store.TaskDir("HERE"), "nested")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	t.Setenv("TSPACE_TASK", "STALE")
	for _, name := range []string{"", "."} {
		got, err := h.app.resolveTask(name)
		if err != nil || got != "HERE" {
			t.Fatalf("resolve(%q)=%q %v", name, got, err)
		}
	}
	if err := h.exec(t, h.app.newTaskShowCmd()); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newPathCmd(), ".", "--root"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newEditorCmd(), "--print"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newExecCmd(), "--", "git", "status", "--porcelain"); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range []string{"clean", "stop", "rm"} {
		var err error
		switch cmd {
		case "clean":
			err = h.exec(t, h.app.newCleanCmd(), ".")
		case "stop":
			err = h.exec(t, h.app.newStopCmd(), ".")
		case "rm":
			err = h.exec(t, h.app.newTaskRmCmd(), ".")
		}
		if err == nil || !strings.Contains(err.Error(), "explicitly") {
			t.Fatalf("%s: %v", cmd, err)
		}
	}
	t.Chdir(t.TempDir())
	if _, err := h.app.resolveTask("."); err == nil || !strings.Contains(err.Error(), "name a task") {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newTaskShowCmd()); err == nil || !strings.Contains(err.Error(), "not inside a task") {
		t.Fatal(err)
	}
}
