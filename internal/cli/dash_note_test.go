package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/wuyuz99/taskspace/internal/task"
	"github.com/wuyuz99/taskspace/internal/workspace"
)

// screen is what the view puts on the terminal, unstyled, failing if a line
// would wrap or the view would run off the alt screen.
func screen(t *testing.T, m dashModel) string {
	t.Helper()
	view := ansi.Strip(m.View())
	lines := strings.Split(view, "\n")
	for _, l := range lines {
		if ansi.StringWidth(l) > m.width {
			t.Fatalf("line wider than %d: %q", m.width, l)
		}
	}
	if len(lines) > m.height {
		t.Fatalf("%d lines on a screen of %d:\n%s", len(lines), m.height, view)
	}
	return view
}

// unwrapped undoes the note's wrapping, for text that may break anywhere.
func unwrapped(view string) string { return strings.NewReplacer("\n", "", " ", "").Replace(view) }

// dirtyDash is a task of several repositories, each with work to lose.
func dirtyDash(t *testing.T) (*harness, dashModel, *task.Task) {
	t.Helper()
	h := newHarness(t)
	h.startedTask(t, "T")
	for range 2 {
		if err := h.exec(t, h.app.newRepoCmd(), "add", "T", mkRepo(t)); err != nil {
			t.Fatalf("repo add: %v", err)
		}
	}
	if err := h.exec(t, h.app.newStartCmd(), "T", "--no-runtime"); err != nil {
		t.Fatalf("start: %v", err)
	}
	tk := h.load(t, "T")
	for _, r := range tk.Repos {
		if err := os.WriteFile(filepath.Join(r.Worktree, "dirty"), []byte("keep"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	m := newDashModel(t.Context(), h.app, time.Second)
	m = drive(t, m, dashDataMsg(h.app.dashLoad(t.Context())))
	return h, m, tk
}

func TestDashRefusalIsReadWhole(t *testing.T) {
	for _, tc := range []struct {
		key, command string
	}{
		{"D", "tspace clean T --force"},
		{"R", "tspace clean T --rm --force"},
	} {
		t.Run(tc.key, func(t *testing.T) {
			_, m, tk := dirtyDash(t)
			m = drive(t, m, key(tc.key))
			m = drive(t, m, key("y"))
			if !m.noteErr {
				t.Fatalf("not refused: %q", m.note)
			}
			view := screen(t, m)
			for _, want := range []string{"refusing to clean T", tc.command, "commit, push or stash it"} {
				if !strings.Contains(unwrapped(view), unwrapped(want)) {
					t.Fatalf("%q missing:\n%s", want, view)
				}
			}
			if strings.Contains(unwrapped(view), unwrapped(workspace.ForceAdvice)) {
				t.Fatalf("advice dash cannot follow:\n%s", view)
			}
			for name, r := range tk.Repos {
				if !strings.Contains(unwrapped(view), unwrapped(name+":")) || !strings.Contains(unwrapped(view), unwrapped(r.Worktree)) {
					t.Fatalf("%s missing:\n%s", name, view)
				}
			}
		})
	}
}

// The refusal of - names the command that finishes the job.
func TestDashRemoveRepoRefusalNamesRepoRm(t *testing.T) {
	h := newHarness(t)
	tk := h.startedRootTask(t, "T")
	repo := tk.RepoNames()[0]
	if err := os.WriteFile(filepath.Join(tk.Repos[repo].Worktree, "dirty"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	m := newDashModel(t.Context(), h.app, time.Second)
	m.expanded = map[string]bool{"T": true}
	m = drive(t, m, dashDataMsg(h.app.dashLoad(t.Context())))
	m.cursor = 1
	m = drive(t, m, key("-"))
	m = drive(t, m, key("y"))
	if want := "tspace repo rm T " + repo + " --force"; !strings.Contains(unwrapped(screen(t, m)), unwrapped(want)) {
		t.Fatalf("%q missing:\n%s", want, screen(t, m))
	}
}

// Any long note is wrapped, and the table gives way to it without losing the
// selected row; a key gives the room back.
func TestDashLongNoteShrinksTheTable(t *testing.T) {
	rows := make([]dashRow, 30)
	for i := range rows {
		rows[i] = dashRow{Task: fmt.Sprintf("T%02d", i), Window: "repo", Dir: "/w", Git: "clean"}
	}
	m := newDashModel(t.Context(), nil, time.Second)
	m = drive(t, m, dashDataMsg(dashData{Rows: rows, Tasks: map[string]dashTask{
		"T20": {Handoff: task.Note{Text: "one\ntwo\nthree"}, Title: "title", Ticket: "X-1"},
	}}))
	m.cursor = 20
	long := strings.Repeat("word ", 40)
	m = noted(m, dashDoneMsg{err: errors.New(long + "\n\nfirst\n" + strings.Repeat("x", 100))})
	view := screen(t, m)
	if strings.Count(unwrapped(view), "word") != 40 || !strings.Contains(unwrapped(view), strings.Repeat("x", 100)) {
		t.Fatalf("note cut:\n%s", view)
	}
	if !strings.Contains(view, "> T20") {
		t.Fatalf("selected row scrolled off:\n%s", view)
	}

	m = drive(t, m, key("j"))
	view = screen(t, m)
	if m.note != "" || strings.Contains(view, "word") || !strings.Contains(view, "> T21") {
		t.Fatalf("note stayed: %q\n%s", m.note, view)
	}
}

// A one-line note is left alone by a key, as it always was.
func TestDashShortNoteStays(t *testing.T) {
	m := newDashModel(t.Context(), nil, time.Second)
	m = drive(t, m, dashDataMsg(dashData{Rows: []dashRow{{Task: "A", Window: "r"}, {Task: "B", Window: "r"}}}))
	m = noted(m, dashDoneMsg{text: "fine"})
	m = drive(t, m, key("j"))
	if view := screen(t, m); !strings.Contains(view, "fine") {
		t.Fatalf("short note went:\n%s", view)
	}
}

// A note taller than the screen keeps its start and its advice.
func TestDashNoteTallerThanTheScreen(t *testing.T) {
	m := newDashModel(t.Context(), nil, time.Second)
	m = drive(t, m, dashDataMsg(dashData{Rows: []dashRow{{Task: "A", Window: "r"}}}))
	var lost []string
	for i := range 40 {
		lost = append(lost, fmt.Sprintf("repo%d: changes", i))
	}
	m = noted(m, dashDoneMsg{err: errors.New("refusing\n\n" + strings.Join(lost, "\n") + "\n\n" + workspace.ForceAdvice)})
	view := screen(t, m)
	for _, want := range []string{"refusing", workspace.ForceAdvice, "more lines"} {
		if !strings.Contains(view, want) {
			t.Fatalf("%q missing:\n%s", want, view)
		}
	}
}

// noted is an action's outcome arriving, without the refresh that follows it.
func noted(m dashModel, msg dashDoneMsg) dashModel {
	next, _ := m.Update(msg)
	return next.(dashModel)
}

// The advice survives the cut whole however it wraps, and a click, unlike the
// wheel or esc, leaves the note where it is; esc doesn't quit from under it.
func TestDashTallNoteKeepsItsAdviceAndYieldsToKeys(t *testing.T) {
	m := newDashModel(t.Context(), nil, time.Second)
	m = drive(t, m, dashDataMsg(dashData{Rows: []dashRow{{Task: "A", Window: "r"}}}))
	m.width = 30
	advice := strings.TrimSpace(strings.Repeat("advice ", 20))
	note := "  lost\tone\n" + strings.Repeat("repo\n", 30) + advice
	m = noted(m, dashDoneMsg{err: errors.New(note)})
	view := screen(t, m)
	if !strings.Contains(unwrapped(view), unwrapped(advice)) || !strings.Contains(view, "  lost one") {
		t.Fatalf("note damaged:\n%s", view)
	}

	next, _ := m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if m = next.(dashModel); m.note == "" {
		t.Fatal("a click dropped the note")
	}
	next, cmd := m.Update(key("esc"))
	if m = next.(dashModel); m.note != "" || cmd != nil {
		t.Fatalf("esc: note=%q cmd=%v", m.note, cmd != nil)
	}
	if _, cmd = m.Update(key("esc")); cmd == nil {
		t.Fatal("esc with no note does not quit")
	}
}

// repo rm --force does what the refusal says: the work goes and so does the
// repository. --keep-worktree, which keeps the work, can't go with it.
func TestRepoRmForce(t *testing.T) {
	h := newHarness(t)
	tk := h.startedTask(t, "T")
	repo := tk.RepoNames()[0]
	if err := os.WriteFile(filepath.Join(tk.Repos[repo].Worktree, "dirty"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newRepoCmd(), "rm", "T", repo); err == nil || !strings.Contains(err.Error(), workspace.ForceAdvice) {
		t.Fatalf("refusal: %v", err)
	}
	if err := h.exec(t, h.app.newRepoCmd(), "rm", "T", repo, "--force", "--keep-worktree"); err == nil {
		t.Fatal("--force with --keep-worktree accepted")
	}
	if err := h.exec(t, h.app.newRepoCmd(), "rm", "T", repo, "--force"); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.load(t, "T").Repo(repo); ok {
		t.Fatal("repository kept")
	}
	if _, err := os.Stat(tk.Repos[repo].Worktree); !os.IsNotExist(err) {
		t.Fatal("worktree kept")
	}
}
