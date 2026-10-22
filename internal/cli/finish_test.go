package cli

import (
	"os"
	osexec "os/exec"
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

// A task emptied by repo rm may still have a session and worktrees on disk:
// clean --rm stops the one and keeps the record while the others remain.
func TestCleanRmOfEmptiedTask(t *testing.T) {
	h := newHarness(t)
	tk := h.startedTask(t, "PAY-1")
	repo := tk.RepoNames()[0]
	if err := h.exec(t, h.app.newRepoCmd(), "rm", "PAY-1", repo, "--keep-worktree"); err != nil {
		t.Fatal(err)
	}
	rt := &layoutRuntime{running: true}
	h.app.Runtime = rt
	err := h.exec(t, h.app.newCleanCmd(), "PAY-1", "--rm")
	if err == nil || !strings.Contains(err.Error(), "kept the record of PAY-1") || !strings.Contains(err.Error(), repo) {
		t.Fatalf("clean --rm = %v", err)
	}
	if !rt.stopped {
		t.Error("session left running")
	}
	if !h.app.Store.Exists("PAY-1") {
		t.Fatal("record deleted while a worktree is left")
	}
}

// A branch --delete-branch had to keep outlives the record, and clean says so.
func TestCleanRmWarnsAboutKeptBranch(t *testing.T) {
	h := newHarness(t)
	tk := h.startedTask(t, "PAY-1")
	wt := tk.Repos[tk.RepoNames()[0]].Worktree
	if err := os.WriteFile(wt+"/new.txt", []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "new.txt"}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "unpushed"}} {
		c := osexec.Command("git", args...)
		c.Dir = wt
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := h.exec(t, h.app.newCleanCmd(), "PAY-1", "--delete-branch", "--rm"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.errb.String(), "kept branch") || !strings.Contains(h.errb.String(), "no task will refer to it") {
		t.Fatalf("stderr:\n%s", h.errb.String())
	}
}
