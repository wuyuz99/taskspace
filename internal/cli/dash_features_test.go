package cli

import (
	"os"
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
	rows[3].Signal = &task.Signal{State: task.SignalBlocked}
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
	m.table(&b, time.Now())
	if !strings.Contains(b.String(), "Long title") || !strings.Contains(b.String(), "clean") {
		t.Fatal(b.String())
	}
	for _, line := range strings.Split(b.String(), "\n") {
		if len([]rune(line)) > 110 {
			t.Fatalf("wide line %s", line)
		}
	}
}
