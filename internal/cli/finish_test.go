package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/wuyuz99/taskspace/internal/task"
)

func TestTaskCreateStart(t *testing.T) {
	h := newHarness(t)
	rt := &layoutRuntime{}
	h.app.Runtime = rt
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-1", "--agent", "claude", "--repo", "backend="+h.repo, "--start"); err != nil {
		t.Fatal(err)
	}
	tk := h.load(t, "PAY-1")
	if _, err := os.Stat(tk.Repos["backend"].Worktree); err != nil {
		t.Fatalf("worktree not created: %v", err)
	}
	if _, ok := rt.window("backend"); !ok {
		t.Fatalf("session not started: %+v", rt.started)
	}
	if !strings.Contains(h.out.String(), "session tspace-PAY-1 is up") {
		t.Fatalf("out:\n%s", h.out.String())
	}
}

func TestTaskCreateStartNeedsARepo(t *testing.T) {
	h := newHarness(t)
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-1", "--start"); err == nil || !strings.Contains(err.Error(), "--start needs a repository") {
		t.Fatalf("create --start = %v", err)
	}
	if h.app.Store.Exists("PAY-1") {
		t.Fatal("refused create still made the task")
	}
}

// A start that fails leaves the task created, and says so.
func TestTaskCreateStartFailureKeepsTheTask(t *testing.T) {
	h := newHarness(t)
	h.app.Runtime = &openRuntime{startErr: os.ErrPermission}
	err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-1", "--repo", "backend="+h.repo, "--start")
	if err == nil || !strings.Contains(err.Error(), "was created, but starting it failed") {
		t.Fatalf("create --start = %v", err)
	}
	if !h.app.Store.Exists("PAY-1") {
		t.Fatal("task gone after a failed start")
	}
}

func TestCleanRmFinishesTheTask(t *testing.T) {
	h := newHarness(t)
	tk := h.startedTask(t, "PAY-1")
	if err := h.app.Store.WriteNote("PAY-1", task.BriefFile, "# brief\n"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newCleanCmd(), "PAY-1", "--rm"); err != nil {
		t.Fatal(err)
	}
	if h.app.Store.Exists("PAY-1") {
		t.Fatal("record still there")
	}
	for _, p := range []string{tk.Repos[tk.RepoNames()[0]].Worktree, h.app.Store.TaskDir("PAY-1")} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s still there: %v", p, err)
		}
	}
	if !strings.Contains(h.out.String(), "removed task PAY-1") || !strings.Contains(h.out.String(), "TASK.md") {
		t.Fatalf("out:\n%s", h.out.String())
	}
}

// A refused clean must not delete the record either.
func TestCleanRmRefusedKeepsTheRecord(t *testing.T) {
	h := newHarness(t)
	tk := h.startedTask(t, "PAY-1")
	if err := os.WriteFile(tk.Repos[tk.RepoNames()[0]].Worktree+"/wip.txt", []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newCleanCmd(), "PAY-1", "--rm"); err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("clean --rm = %v", err)
	}
	if !h.app.Store.Exists("PAY-1") {
		t.Fatal("refused clean deleted the record")
	}
}

func TestCleanRmOfTaskWithoutRepos(t *testing.T) {
	h := newHarness(t)
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-1"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newCleanCmd(), "PAY-1", "--rm"); err != nil {
		t.Fatal(err)
	}
	if h.app.Store.Exists("PAY-1") {
		t.Fatal("record still there")
	}
}
