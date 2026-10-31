package cli

import (
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wuyuz99/taskspace/internal/status"
	"github.com/wuyuz99/taskspace/internal/task"
)

func TestDashCollapseAndExpand(t *testing.T) {
	rows := []dashRow{
		{Task: "A", Window: task.RootWindowName, Runtime: status.RuntimeRunning},
		{Task: "A", Window: "backend", Runtime: status.RuntimeRunning, Git: "clean"},
		{Task: "B", Window: task.RootWindowName, Runtime: status.RuntimeRunning},
		{Task: "B", Window: "backend", Runtime: status.RuntimeDead},
	}
	m := newDashModel(t.Context(), nil, time.Second)
	m = drive(t, m, dashDataMsg(dashData{Rows: rows}))
	if len(m.data.Rows) != 3 || m.data.Rows[0].Summary != 2 || m.data.Rows[0].Window != task.RootWindowName {
		t.Fatalf("rows %+v", m.data.Rows)
	}
	m = drive(t, m, key(" "))
	if len(m.data.Rows) != 4 {
		t.Fatal("space did not expand")
	}
	m = drive(t, m, key("j"))
	m = drive(t, m, key("left"))
	if len(m.data.Rows) != 3 || m.cursor != 0 {
		t.Fatal("collapse lost selection")
	}
	rows[3].Runtime = status.RuntimeRunning
	rows[3].Report = &status.Report{State: status.ReportQuiet, Source: status.SourceSilence}
	m = drive(t, m, dashDataMsg(dashData{Rows: rows}))
	if len(m.data.Rows) != 2 {
		t.Fatal("a quiet window kept its task open")
	}
	rows[3].Report = &status.Report{State: status.ReportAttention, Source: status.SourceBell}
	m = drive(t, m, dashDataMsg(dashData{Rows: rows}))
	if len(m.data.Rows) != 3 {
		t.Fatal("ringing task was collapsed")
	}
	rows[3].Report = &status.Report{State: status.ReportState(task.SignalBlocked), Source: status.SourceSignal}
	m = drive(t, m, dashDataMsg(dashData{Rows: rows}))
	if len(m.data.Rows) != 3 {
		t.Fatal("blocked task was collapsed")
	}
}

func TestDashCollapsedAttachUsesRootRuntime(t *testing.T) {
	h := newHarness(t)
	m := newDashModel(t.Context(), h.app, time.Second)
	m = drive(t, m, dashDataMsg(dashData{Rows: []dashRow{
		{Task: "A", Window: task.RootWindowName, Runtime: status.RuntimeRunning},
		{Task: "A", Window: "backend", Runtime: status.RuntimeNoWindow},
	}}))
	_, cmd := m.Update(key("enter"))
	if cmd == nil {
		t.Fatal("missing sibling blocked attaching to root")
	}
	if msg, ok := cmd().(dashAttachMsg); !ok || msg.Window != task.RootWindowName {
		t.Fatalf("attach=%+v", msg)
	}
}

func TestDashCleanConfirmsAndPreservesDirtyWork(t *testing.T) {
	h, m := loadedDash(t)
	tk := h.load(t, m.data.Rows[0].Task)
	wt := tk.Repos[tk.RepoNames()[0]].Worktree
	if err := os.WriteFile(filepath.Join(wt, "dirty"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	m = drive(t, m, key("D"))
	if m.confirm == nil || !strings.Contains(m.confirm.prompt, tk.Name) || !strings.Contains(m.confirm.prompt, "worktrees") {
		t.Fatal("missing confirmation")
	}
	m = drive(t, m, key("y"))
	if !m.noteErr || !strings.Contains(m.note, "refusing to clean") {
		t.Fatal(m.note)
	}
	if _, err := os.Stat(wt); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(wt, "dirty")); err != nil {
		t.Fatal(err)
	}
	m = drive(t, m, key("D"))
	m = drive(t, m, key("y"))
	if m.noteErr {
		t.Fatal(m.note)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatal("worktree not cleaned")
	}
	if !h.app.Store.Exists(tk.Name) {
		t.Fatal("clean removed record")
	}
}

func TestDashTitleUsesSpareWidth(t *testing.T) {
	m := newDashModel(t.Context(), nil, time.Second)
	m.width = 110
	m.data = dashData{Rows: []dashRow{{Task: "PAY-1", Window: "api", Runtime: status.RuntimeRunning, Git: "clean"}}, Tasks: map[string]dashTask{"PAY-1": {Title: strings.Repeat("Long title ", 50)}}}
	var b strings.Builder
	m.table(&b, time.Now(), 0)
	if !strings.Contains(b.String(), "Long title") || !strings.Contains(b.String(), "clean") {
		t.Fatal(b.String())
	}
	for _, line := range strings.Split(b.String(), "\n") {
		if len([]rune(line)) > 110 {
			t.Fatalf("wide line %s", line)
		}
	}
}

// R deletes the task, record and notes, after confirming; refusals change
// nothing, and the cursor moves to the next task.
func TestDashDeleteTask(t *testing.T) {
	h, m := loadedDash(t)
	tk := h.load(t, m.data.Rows[0].Task)
	next := ""
	for _, r := range m.data.Rows {
		if r.Task != tk.Name {
			next = r.Task
			break
		}
	}
	wt := tk.Repos[tk.RepoNames()[0]].Worktree
	if err := os.WriteFile(filepath.Join(wt, "dirty"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	m = drive(t, m, key("R"))
	if m.confirm == nil || !strings.Contains(m.confirm.prompt, "delete "+tk.Name) || !strings.Contains(m.confirm.prompt, "notes") {
		t.Fatalf("confirm = %+v", m.confirm)
	}
	m = drive(t, m, key("n"))
	if m.note != "cancelled" || !h.app.Store.Exists(tk.Name) {
		t.Fatalf("n did not cancel: %q", m.note)
	}
	m = drive(t, m, key("R"))
	m = drive(t, m, key("y"))
	if !m.noteErr || !strings.Contains(m.note, "refusing to clean") || !h.app.Store.Exists(tk.Name) {
		t.Fatalf("dirty delete: %q", m.note)
	}
	if err := os.Remove(filepath.Join(wt, "dirty")); err != nil {
		t.Fatal(err)
	}
	m = drive(t, m, key("R"))
	m = drive(t, m, key("y"))
	if m.noteErr || !strings.Contains(m.note, "deleted "+tk.Name) {
		t.Fatal(m.note)
	}
	if h.app.Store.Exists(tk.Name) {
		t.Fatal("record kept")
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatal("worktree kept")
	}
	m = drive(t, m, dashDataMsg(h.app.dashLoad(t.Context())))
	if r, ok := m.selected(); !ok || r.Task != next {
		t.Fatalf("cursor on %+v, want %s", r, next)
	}
}

// Deleting the last task moves the cursor to the first row of the one
// before it, and a refresh that still shows the task does not move it.
func TestDashDeleteLastTask(t *testing.T) {
	h, m := loadedDash(t)
	last := m.data.Rows[len(m.data.Rows)-1].Task
	m.cursor = len(m.data.Rows) - 1
	stale := h.app.dashLoad(t.Context())
	m = drive(t, m, key("R"))
	m = drive(t, m, key("y"))
	if m.noteErr {
		t.Fatal(m.note)
	}
	m = drive(t, m, dashDataMsg(stale))
	if m.gone != last {
		t.Fatal("a stale refresh forgot the deleted task")
	}
	m = drive(t, m, dashDataMsg(h.app.dashLoad(t.Context())))
	r, ok := m.selected()
	if !ok || r.Task == last || (m.cursor > 0 && m.data.Rows[m.cursor-1].Task == r.Task) {
		t.Fatalf("cursor %d on %+v", m.cursor, r)
	}
}

// Inside a task's own session, D and R would end the dash halfway.
func TestDashCleanRefusedInsideTask(t *testing.T) {
	h, m := loadedDash(t)
	name := m.data.Rows[0].Task
	t.Setenv(envTask, name)
	for _, k := range []string{"D", "R"} {
		m = drive(t, m, key(k))
		if m.confirm != nil || !m.noteErr || !strings.Contains(m.note, "from outside") {
			t.Fatalf("%s: confirm=%+v note=%q", k, m.confirm, m.note)
		}
	}
	if !h.app.Store.Exists(name) {
		t.Fatal("task removed")
	}
}

// - removes the selected repository, as repo rm does: refused while its
// work is uncommitted, then its worktree goes and its branch stays.
func TestDashRemoveRepo(t *testing.T) {
	h := newHarness(t)
	tk := h.startedRootTask(t, "T")
	repo := tk.RepoNames()[0]
	wt := tk.Repos[repo].Worktree
	m := newDashModel(t.Context(), h.app, time.Second)
	m.expanded = map[string]bool{"T": true}
	m = drive(t, m, dashDataMsg(h.app.dashLoad(t.Context())))
	if len(m.data.Rows) != 2 || !m.data.Rows[0].root() {
		t.Fatalf("rows %+v", m.data.Rows)
	}
	m = drive(t, m, key("-"))
	if m.confirm != nil || !strings.Contains(m.note, "root window isn't a repository") {
		t.Fatalf("- on the root row: %q", m.note)
	}

	m.cursor = 1
	if err := os.WriteFile(filepath.Join(wt, "dirty"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	m = drive(t, m, key("-"))
	if m.confirm == nil || !strings.Contains(m.confirm.prompt, "remove "+repo+" from T") {
		t.Fatalf("confirm = %+v", m.confirm)
	}
	m = drive(t, m, key("y"))
	if !m.noteErr || !strings.Contains(m.note, "refusing to clean") {
		t.Fatalf("dirty: %q", m.note)
	}
	if _, ok := h.load(t, "T").Repo(repo); !ok {
		t.Fatal("refused rm forgot the repository")
	}

	if err := os.Remove(filepath.Join(wt, "dirty")); err != nil {
		t.Fatal(err)
	}
	saved := h.load(t, "T")
	saved.Windows = []task.Window{{Name: "git", Repo: repo}}
	if err := h.app.Store.Save(saved); err != nil {
		t.Fatal(err)
	}
	m = drive(t, m, key("-"))
	m = drive(t, m, key("y"))
	if m.noteErr || !strings.Contains(m.note, "removed "+repo+" from T") || !strings.Contains(m.note, "forgot saved window _git") {
		t.Fatal(m.note)
	}
	if _, ok := h.load(t, "T").Repo(repo); ok {
		t.Fatal("repository kept")
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatal("worktree kept")
	}
	if out, err := osexec.Command("git", "-C", h.repo, "branch", "--list", tk.Repos[repo].Branch).Output(); err != nil || len(strings.TrimSpace(string(out))) == 0 {
		t.Fatalf("branch gone: %q %v", out, err)
	}
}

func TestDashRemoveRepoOnCollapsedTask(t *testing.T) {
	m := newDashModel(t.Context(), nil, time.Second)
	m = drive(t, m, dashDataMsg(dashData{Rows: []dashRow{
		{Task: "A", Window: task.RootWindowName, Runtime: status.RuntimeRunning},
		{Task: "A", Window: "backend", Runtime: status.RuntimeRunning},
	}}))
	m = drive(t, m, key("-"))
	if m.confirm != nil || !strings.Contains(m.note, "expand the task") {
		t.Fatalf("note %q", m.note)
	}
}

func TestDashRemoveRepoRefusedInsideTask(t *testing.T) {
	h := newHarness(t)
	h.startedTask(t, "T")
	m := newDashModel(t.Context(), h.app, time.Second)
	m = drive(t, m, dashDataMsg(h.app.dashLoad(t.Context())))
	t.Setenv(envTask, "T")
	m = drive(t, m, key("-"))
	if m.confirm != nil || !strings.Contains(m.note, "from outside") {
		t.Fatalf("note %q", m.note)
	}
}
