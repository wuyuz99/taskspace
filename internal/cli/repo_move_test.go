package cli

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestRepoMoveCommand(t *testing.T) {
	h := newHarness(t)
	tk := h.startedTask(t, "OLD")
	repo := tk.RepoNames()[0]
	if _, err := h.app.Store.Create("NEW"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newRepoCmd(), "move", "OLD", repo); err == nil || !strings.Contains(err.Error(), "--to") {
		t.Fatalf("without --to: %v", err)
	}
	if err := h.exec(t, h.app.newRepoCmd(), "move", ".", repo, "--to", "NEW"); err == nil || !strings.Contains(err.Error(), "explicitly") {
		t.Fatalf("with '.': %v", err)
	}
	if err := h.exec(t, h.app.newRepoCmd(), "move", "OLD", repo, "--to", "NEW", "--name", "api"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.out.String(), "moved "+repo+" from OLD to NEW as api") || !strings.Contains(h.out.String(), "tspace start NEW api") {
		t.Fatalf("output: %s", h.out.String())
	}
	got := h.load(t, "NEW").Repos["api"]
	if got.Branch != tk.Repos[repo].Branch || got.Worktree != h.app.Store.WorktreePath("NEW", "api") {
		t.Fatalf("repo: %+v", got)
	}
	// The agent had the first repository's worktree, and goes back to the root.
	if from := h.load(t, "OLD"); len(from.Repos) != 0 || from.AgentRepo != "" {
		t.Fatalf("old: %+v", from)
	}
	if _, err := os.Stat(got.Worktree); err != nil {
		t.Fatal(err)
	}
}

// A task gaining its second repository while running opens its root window,
// as repo add does.
func TestRepoMoveOpensTheRootWindow(t *testing.T) {
	h := newHarness(t)
	tk := h.startedRootTask(t, "OLD")
	repo := tk.RepoNames()[0]
	tk.AgentRepo = ""
	if err := h.app.Store.Save(tk); err != nil {
		t.Fatal(err)
	}
	other := mkRepo(t)
	if err := h.exec(t, h.app.newTaskCmd(), "create", "NEW", "--repo", "other="+other); err != nil {
		t.Fatal(err)
	}
	rt := &layoutRuntime{running: true}
	h.app.Runtime = rt
	if err := h.exec(t, h.app.newRepoCmd(), "move", "OLD", repo, "--to", "NEW"); err != nil {
		t.Fatal(err)
	}
	if len(rt.opened) != 1 || !strings.Contains(h.out.String(), "opened the root window for NEW") {
		t.Fatalf("opened %+v; output %s", rt.opened, h.out.String())
	}
}

func TestDashMoveRepo(t *testing.T) {
	h := newHarness(t)
	tk := h.startedRootTask(t, "OLD")
	repo := tk.RepoNames()[0]
	tk.AgentRepo = ""
	if err := h.app.Store.Save(tk); err != nil {
		t.Fatal(err)
	}
	if _, err := h.app.Store.Create("NEW"); err != nil {
		t.Fatal(err)
	}
	m := newDashModel(t.Context(), h.app, time.Second)
	m.expanded = map[string]bool{"OLD": true}
	m = drive(t, m, dashDataMsg(h.app.dashLoad(t.Context())))
	cursor := -1
	for i, r := range m.data.Rows {
		if r.Task == "OLD" && r.Window == repo {
			cursor = i
		}
	}
	if cursor < 0 {
		t.Fatalf("rows %+v", m.data.Rows)
	}
	m.cursor = cursor - 1
	m = drive(t, m, key("m"))
	if m.form != nil || !strings.Contains(m.note, "root window isn't a repository") {
		t.Fatalf("m on the root row: %q", m.note)
	}
	m.cursor = cursor
	m = drive(t, m, key("m"))
	if m.form == nil {
		t.Fatal("move form did not open")
	}
	m = drive(t, m, key("enter"))
	if m.form == nil || !strings.Contains(m.form.note, "pick a task") {
		t.Fatalf("nothing picked: %+v", m.form)
	}
	m = drive(t, m, key(" "))
	m = drive(t, m, key("enter"))
	if m.form != nil || m.noteErr || !strings.Contains(m.note, "moved "+repo+" from OLD to NEW") {
		t.Fatalf("note: %q", m.note)
	}
	if _, ok := h.load(t, "NEW").Repo(repo); !ok {
		t.Fatal("dash did not move it")
	}
	m = drive(t, m, dashDataMsg(h.app.dashLoad(t.Context())))
	if row, ok := m.selected(); !ok || row.Task != "NEW" {
		t.Fatalf("selected %+v", row)
	}
}
