package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wuyuz99/taskspace/internal/task"
)

func TestProjectRecipeSnapshotsConfigurationOnly(t *testing.T) {
	h := newHarness(t)
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-142", "--title", "Private title"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newRepoCmd(), "add", "PAY-142", h.repo, "--name", "backend", "--branch", "custom-branch", "--agent", "sleep", "--agent", "60", "--post-create", "echo setup"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newTaskCmd(), "handoff", "PAY-142", "Private progress"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newProjectCmd(), "save", "payments", "--from", "PAY-142"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newProjectCmd(), "save", "payments", "--from", "PAY-142"); err == nil || !strings.Contains(err.Error(), "--replace") {
		t.Fatalf("overwrite not guarded: %v", err)
	}
	if err := h.exec(t, h.app.newProjectCmd(), "show", "payments"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.out.String(), "echo setup") {
		t.Fatal("show hides hook")
	}
	if err := h.exec(t, h.app.newProjectCmd(), "list"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.out.String(), "payments") {
		t.Fatal("list missing recipe")
	}
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-156", "--project", "payments"); err != nil {
		t.Fatal(err)
	}
	tk, err := h.app.Store.Load("PAY-156")
	if err != nil {
		t.Fatal(err)
	}
	r := tk.Repos["backend"]
	if r.Branch != "agent/PAY-156" || r.Worktree != h.app.Store.WorktreePath("PAY-156", "backend") || r.Base != "main" || strings.Join(r.Agent, ",") != "sleep,60" || r.PostCreate != "echo setup" {
		t.Fatalf("bad snapshot: %+v", r)
	}
	if tk.Title != "" || tk.Ticket != "" {
		t.Fatal("task context leaked")
	}
	// A recipe carries configuration, never the source task's prose.
	for _, file := range []string{task.BriefFile, task.HandoffFile} {
		if note, err := h.app.Store.ReadNote("PAY-156", file); err != nil || note.Text != "" {
			t.Fatalf("recipe carried %s across: %q (%v)", file, note.Text, err)
		}
	}
	if _, err := os.Stat(r.Worktree); !os.IsNotExist(err) {
		t.Fatal("recipe application created worktrees")
	}
	source, err := h.app.Store.Load("PAY-142")
	if err != nil {
		t.Fatal(err)
	}
	sr := source.Repos["backend"]
	sr.PostCreate = "echo changed"
	source.SetRepo("backend", sr)
	if err := h.app.Store.Save(source); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newProjectCmd(), "save", "payments", "--from", "PAY-142", "--replace"); err != nil {
		t.Fatal(err)
	}
	tk, err = h.app.Store.Load("PAY-156")
	if err != nil {
		t.Fatal(err)
	}
	if tk.Repos["backend"].PostCreate != "echo setup" {
		t.Fatal("replacement mutated existing task")
	}
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-157", "--project", "payments"); err != nil {
		t.Fatal(err)
	}
	tk, err = h.app.Store.Load("PAY-157")
	if err != nil {
		t.Fatal(err)
	}
	if tk.Repos["backend"].PostCreate != "echo changed" {
		t.Fatal("new task missed recipe update")
	}
}

func TestInvalidProjectDoesNotLeavePartialTask(t *testing.T) {
	h := newHarness(t)
	p := &task.Project{Name: "broken", Repos: map[string]task.ProjectRepo{"backend": {Path: h.repo}, "missing": {Path: filepath.Join(t.TempDir(), "absent")}}}
	if err := h.app.Store.SaveProject(p, false); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"broken", "not-found"} {
		if err := h.exec(t, h.app.newTaskCmd(), "create", "new-task", "--project", name); err == nil {
			t.Fatal("invalid recipe accepted")
		}
		if h.app.Store.Exists("new-task") {
			t.Fatal("partial task left behind")
		}
	}
}

func TestProjectAdditionalRepositoriesAndDuplicateProtection(t *testing.T) {
	h := newHarness(t)
	p := &task.Project{Name: "payments", Repos: map[string]task.ProjectRepo{"backend": {Path: h.repo}}}
	if err := h.app.Store.SaveProject(p, false); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newTaskCmd(), "create", "duplicate", "--project", "payments", "--repo", "backend="+h.repo); err == nil {
		t.Fatal("duplicate accepted")
	}
	if h.app.Store.Exists("duplicate") {
		t.Fatal("partial task saved")
	}
	extra := mkRepo(t)
	if err := h.exec(t, h.app.newTaskCmd(), "create", "extended", "--project", "payments", "--repo", "frontend="+extra); err != nil {
		t.Fatal(err)
	}
	tk, err := h.app.Store.Load("extended")
	if err != nil {
		t.Fatal(err)
	}
	if len(tk.Repos) != 2 {
		t.Fatal("additional repo missing")
	}
}
