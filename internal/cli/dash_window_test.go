package cli

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/wuyuz99/taskspace/internal/runtime"
	"github.com/wuyuz99/taskspace/internal/task"
)

func TestSplitWords(t *testing.T) {
	for in, want := range map[string][]string{
		"":                       nil,
		"  htop ":                {"htop"},
		`make test ARGS='-v -x'`: {"make", "test", "ARGS=-v -x"},
		`echo "a \"b\"" c\ d`:    {"echo", `a "b"`, "c d"},
		`''`:                     {""},
		`grep -P "\d+" "a\$b"`:   {"grep", "-P", `\d+`, "a$b"},
	} {
		got, err := splitWords(in)
		if err != nil || !slices.Equal(got, want) {
			t.Errorf("splitWords(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := splitWords(`echo "open`); err == nil {
		t.Error("unfinished quote accepted")
	}
}

// openingRuntime is a running session whose OpenWindow adds the window.
type openingRuntime struct{ layoutRuntime }

func (r *openingRuntime) OpenWindow(_ context.Context, _ string, w runtime.Window) error {
	r.opened = append(r.opened, w)
	r.started = append(r.started, w)
	return nil
}

func TestDashOpenWindow(t *testing.T) {
	h := newHarness(t)
	tk := h.startedRootTask(t, "T")
	repo := tk.RepoNames()[0]
	rt := &openingRuntime{layoutRuntime{running: true}}
	rt.started = []runtime.Window{{Name: task.RootWindowName}, {Name: repo}}
	h.app.Runtime = rt
	m := newDashModel(t.Context(), h.app, time.Second)
	m.expanded = map[string]bool{"T": true}
	m = drive(t, m, dashDataMsg(h.app.dashLoad(t.Context())))
	m.cursor = 1 // the repository's row

	m = drive(t, m, key("w"))
	if m.form == nil || !strings.Contains(m.form.title, repo+"'s worktree") {
		t.Fatalf("form = %+v", m.form)
	}
	m = drive(t, m, key(`make test ARGS='-v'`))
	m = drive(t, m, tab)
	m = drive(t, m, tab)
	m = drive(t, m, key(" "))
	if !strings.Contains(m.form.note, "needs a name") {
		t.Fatalf("save without a name: %q", m.form.note)
	}
	m = drive(t, m, tea.KeyMsg{Type: tea.KeyShiftTab})
	m = drive(t, m, key("tests"))
	m = drive(t, m, key("enter"))
	if m.form != nil || m.noteErr || !strings.Contains(m.note, "opened _tests") {
		t.Fatalf("form=%v note=%q", m.form != nil, m.note)
	}
	if len(rt.opened) != 1 {
		t.Fatalf("opened %+v", rt.opened)
	}
	w := rt.opened[0]
	if w.Name != "_tests" || w.Dir != tk.Repos[repo].Worktree || !slices.Equal(w.Command, []string{"make", "test", "ARGS=-v"}) {
		t.Fatalf("opened %+v", w)
	}
	if i, ok := h.load(t, "T").SavedWindow("tests"); !ok || h.load(t, "T").Windows[i].Repo != repo {
		t.Fatal("not saved with its repository")
	}
	m = drive(t, m, dashDataMsg(h.app.dashLoad(t.Context())))
	if r, ok := m.selected(); !ok || r.Window != "_tests" {
		t.Fatalf("cursor on %+v", r)
	}

	// From the root row, an unnamed shell at the task root.
	m.cursor = 0
	m = drive(t, m, key("w"))
	m = drive(t, m, key("enter"))
	if m.noteErr || len(rt.opened) != 2 || rt.opened[1].Dir != h.app.Store.TaskDir("T") || len(rt.opened[1].Command) != 0 {
		t.Fatalf("note=%q opened=%+v", m.note, rt.opened)
	}
}

func TestDashOpenWindowNeedsSession(t *testing.T) {
	_, m := loadedDash(t)
	m = drive(t, m, key("w"))
	if m.form != nil || !strings.Contains(m.note, "press s") {
		t.Fatalf("note %q", m.note)
	}
}

func TestWindowLabel(t *testing.T) {
	for in, want := range map[string]string{"g++": "g--", "node@20": "node-20", "make": "make", "": "cmd"} {
		if got := windowLabel(in); got != want || (in != "" && !task.IsExtraWindow("_x-"+got)) {
			t.Errorf("windowLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDashWindowFormChecks(t *testing.T) {
	h, _, repo := extraWindowTask(t) // saves _git in repo
	m := newDashModel(t.Context(), h.app, time.Second)
	f := m.windowForm(dashRow{Task: "T", Window: repo})
	f.field("name").value = []rune("git")
	f.field("save").on = true
	if note := f.check(f); !strings.Contains(note, "already has a saved window") {
		t.Fatalf("saved name: %q", note)
	}
	f.field("name").value = []rune("root")
	if note := f.check(f); !strings.Contains(note, "root window") {
		t.Fatalf("root: %q", note)
	}
	f.field("name").value = []rune("x")
	f.field("command").value = []rune("agent --resume")
	if note := f.check(f); !strings.Contains(note, "full command") {
		t.Fatalf("agent args: %q", note)
	}
	// A saved window's row opens in its repository.
	if got := windowPlace(dashRow{Task: "T", Window: "_git", Saved: true, Repo: repo}); got != repo {
		t.Fatalf("place %q", got)
	}
}

// A refresh that shows the task but not yet the new window keeps waiting.
func TestDashCursorWaitsForNewWindow(t *testing.T) {
	rows := []dashRow{{Task: "T", Window: task.RootWindowName}, {Task: "T", Window: "api"}}
	m := newDashModel(t.Context(), nil, time.Second)
	m = drive(t, m, dashDataMsg(dashData{Rows: rows}))
	next, _ := m.Update(dashDoneMsg{made: "T", window: "_new"})
	m = next.(dashModel)
	m = drive(t, m, dashDataMsg(dashData{Rows: rows}))
	if m.made == "" {
		t.Fatal("stale refresh used up the jump")
	}
	m = drive(t, m, dashDataMsg(dashData{Rows: append(rows, dashRow{Task: "T", Window: "_new"})}))
	if r, _ := m.selected(); r.Window != "_new" {
		t.Fatalf("cursor on %+v", r)
	}
}
