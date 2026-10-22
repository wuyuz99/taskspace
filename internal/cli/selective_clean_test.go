package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/wuyuz99/taskspace/internal/runtime"
	"github.com/wuyuz99/taskspace/internal/task"
)

func TestSelectiveCleanAndRepoRemoval(t *testing.T) {
	h := newHarness(t)
	other := mkRepo(t)
	if err := h.exec(t, h.app.newTaskCmd(), "create", "T", "backend="+h.repo, "frontend="+other); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newStartCmd(), "T", "--no-runtime"); err != nil {
		t.Fatal(err)
	}
	tk := h.load(t, "T")
	tk.Windows = []task.Window{{Name: "logs", Repo: "backend"}}
	if err := h.app.Store.Save(tk); err != nil {
		t.Fatal(err)
	}
	rt := &closingRuntime{layoutRuntime: layoutRuntime{running: true}}
	rt.started = []runtime.Window{{Name: "backend"}, {Name: "frontend"}, {Name: "_logs"}, {Name: task.RootWindowName}}
	h.app.Runtime = rt
	wt := tk.Repos["backend"].Worktree
	if err := os.WriteFile(filepath.Join(wt, "dirty"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newRepoRmCmd(), "T", "backend"); err == nil {
		t.Fatal("dirty removal succeeded")
	}
	if len(rt.closed) != 0 || len(h.load(t, "T").Repos) != 2 {
		t.Fatal("refusal mutated task")
	}
	if err := os.Remove(filepath.Join(wt, "dirty")); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newRepoRmCmd(), "T", "backend"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(rt.closed, []string{"backend", "_logs"}) || rt.stopped {
		t.Fatalf("closed=%v stopped=%v", rt.closed, rt.stopped)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatalf("worktree remains: %v", err)
	}
	if _, err := os.Stat(tk.Repos["frontend"].Worktree); err != nil {
		t.Fatal(err)
	}
	if len(h.load(t, "T").Repos) != 1 {
		t.Fatal("record not updated")
	}
}

func TestSelectiveCleanRefusesUnknownWindows(t *testing.T) {
	h := newHarness(t)
	tk := h.startedTask(t, "T")
	rt := &closingRuntime{layoutRuntime: layoutRuntime{running: true}}
	rt.started = []runtime.Window{{Name: "_unsaved"}}
	h.app.Runtime = rt
	if err := h.exec(t, h.app.newCleanCmd(), "T", tk.RepoNames()[0]); err == nil || !strings.Contains(err.Error(), "no saved repository") {
		t.Fatal(err)
	}
	if len(rt.closed) != 0 {
		t.Fatal("refusal closed windows")
	}
}
