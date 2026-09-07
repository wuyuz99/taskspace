package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestRepoUpdatePreservesWorkspaceAndSnapshots(t *testing.T) {
	h := newHarness(t)
	tk := h.startedTask(t, "PAY-142")
	name := tk.RepoNames()[0]
	before := tk.Repos[name]
	wip := filepath.Join(before.Worktree, "wip.txt")
	if err := os.WriteFile(wip, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	// Updating configuration must not need Git or contact any runtime.
	h.app.Runtime = nil
	h.app.Git = nil
	if err := h.exec(t, h.app.newRepoCmd(), "update", tk.Name, name, "--agent", "codex", "--agent", "--resume", "--post-create", "echo setup"); err != nil {
		t.Fatal(err)
	}
	updated, err := h.app.Store.Load(tk.Name)
	if err != nil {
		t.Fatal(err)
	}
	r := updated.Repos[name]
	if !reflect.DeepEqual(r.Agent, []string{"codex", "--resume"}) || r.PostCreate != "echo setup" {
		t.Fatalf("settings: %+v", r)
	}
	r.Agent, r.PostCreate = before.Agent, before.PostCreate
	if !reflect.DeepEqual(r, before) {
		t.Fatal("update changed repository identity")
	}
	if b, err := os.ReadFile(wip); err != nil || string(b) != "keep" {
		t.Fatal("update affected worktree")
	}
	if err := h.exec(t, h.app.newProjectCmd(), "save", "payments", "--from", tk.Name); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newRepoCmd(), "update", tk.Name, name, "--agent", "sleep", "--agent", "600"); err != nil {
		t.Fatal(err)
	}
	updated, err = h.app.Store.Load(tk.Name)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Repos[name].PostCreate != "echo setup" {
		t.Fatal("agent-only update changed the hook")
	}
	if err := h.exec(t, h.app.newRepoCmd(), "update", tk.Name, name, "--post-create", "echo revised"); err != nil {
		t.Fatal(err)
	}
	updated, err = h.app.Store.Load(tk.Name)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(updated.Repos[name].Agent, []string{"sleep", "600"}) {
		t.Fatal("hook-only update changed the agent")
	}
	if err := h.exec(t, h.app.newRepoCmd(), "update", tk.Name, name, "--shell", "--post-create="); err != nil {
		t.Fatal(err)
	}
	updated, err = h.app.Store.Load(tk.Name)
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Repos[name].Agent) != 0 || updated.Repos[name].PostCreate != "" {
		t.Fatal("clearing failed")
	}
	p, err := h.app.Store.LoadProject("payments")
	if err != nil {
		t.Fatal(err)
	}
	if p.Repos[name].PostCreate != "echo setup" || !reflect.DeepEqual(p.Repos[name].Agent, []string{"codex", "--resume"}) {
		t.Fatal("task update changed saved recipe")
	}
}

func TestRepoUpdateRejectsInvalidFlagsWithoutSaving(t *testing.T) {
	h := newHarness(t)
	tk := h.startedTask(t, "t")
	name := tk.RepoNames()[0]
	for _, flags := range [][]string{nil, {"--agent", ""}, {"--agent", "codex", "--shell"}} {
		args := append([]string{"update", "t", name}, flags...)
		if err := h.exec(t, h.app.newRepoCmd(), args...); err == nil {
			t.Fatalf("accepted %v", flags)
		}
	}
	if err := h.exec(t, h.app.newRepoCmd(), "update", "t", "missing", "--shell"); err == nil {
		t.Fatal("missing repo accepted")
	}
	after, err := h.app.Store.Load("t")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, tk) {
		t.Fatal("failed update changed task")
	}
}
