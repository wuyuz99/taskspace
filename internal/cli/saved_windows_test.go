package cli

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/wuyuz99/taskspace/internal/runtime"
)

// liveRuntime is a session that exists once started, holding the windows it
// was started or opened with, under the names they were last given.
type liveRuntime struct {
	stubRuntime
	panes   map[string]runtime.Window
	renamed [][2]string
}

func (l *liveRuntime) HasSession(context.Context, string) (bool, error) { return l.panes != nil, nil }
func (l *liveRuntime) Start(_ context.Context, _ string, ws []runtime.Window) error {
	if l.panes == nil {
		l.panes = map[string]runtime.Window{}
	}
	for _, w := range ws {
		if _, ok := l.panes[w.Name]; !ok {
			l.panes[w.Name] = w
			l.started = append(l.started, w)
		}
	}
	return nil
}
func (l *liveRuntime) OpenWindow(_ context.Context, _ string, w runtime.Window) error {
	l.panes[w.Name] = w
	return nil
}
func (l *liveRuntime) RenameWindow(_ context.Context, _ string, from, to string) error {
	l.panes[to] = l.panes[from]
	delete(l.panes, from)
	l.renamed = append(l.renamed, [2]string{from, to})
	return nil
}
func (l *liveRuntime) State(context.Context, string) (runtime.SessionState, error) {
	st := runtime.SessionState{Exists: l.panes != nil, Panes: map[string]runtime.PaneState{}}
	for name := range l.panes {
		st.Panes[name] = runtime.PaneState{Window: name}
	}
	return st, nil
}

func TestSavedWindows(t *testing.T) {
	h := newHarness(t)
	rt := &liveRuntime{}
	h.app.Runtime = rt
	other := mkRepo(t)
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-1", "--repo", "backend="+h.repo, "--repo", "frontend="+other); err != nil {
		t.Fatal(err)
	}
	window := func(args ...string) error {
		return h.exec(t, h.app.newWindowCmd(), append([]string{"PAY-1"}, args...)...)
	}

	// Saved with nothing running: only saved, and the worktree need not exist.
	if err := window("backend", "--name", "git", "--save", "--", "lazygit"); err != nil {
		t.Fatal(err)
	}
	if err := window("--root", "--name", "notes", "--save"); err != nil {
		t.Fatal(err)
	}
	if err := window("--name", "git", "--save", "--root"); err == nil || !strings.Contains(err.Error(), "already has a saved window _git") {
		t.Fatalf("saving a name twice = %v", err)
	}
	if err := window("--save", "--", "x"); err == nil {
		t.Fatal("--save accepted without --name")
	}
	if err := window("--root", "--name", "root", "--save"); err == nil {
		t.Fatal("a window saved as the root window")
	}
	if err := window("--rename=", "x"); err == nil || !strings.Contains(err.Error(), "need a window name") {
		t.Fatalf("empty --rename = %v", err)
	}
	if got := h.load(t, "PAY-1").Windows; len(got) != 2 || got[0].Repo != "backend" || !slices.Equal(got[0].Command, []string{"lazygit"}) || got[1].Repo != "" {
		t.Fatalf("saved %+v", got)
	}
	// Saved windows alone need version 2, not the current version.
	if v := h.load(t, "PAY-1").Version; v != 2 {
		t.Fatalf("record version %d", v)
	}

	// Starting one repository opens only its saved windows.
	if err := h.exec(t, h.app.newStartCmd(), "PAY-1", "frontend"); err != nil {
		t.Fatal(err)
	}
	if _, ok := rt.panes["_git"]; ok {
		t.Fatal("start frontend opened backend's saved window")
	}
	// The whole task opens all of them, where they belong.
	if err := h.exec(t, h.app.newStartCmd(), "PAY-1"); err != nil {
		t.Fatal(err)
	}
	git, notes := rt.panes["_git"], rt.panes["_notes"]
	tk := h.load(t, "PAY-1")
	if git.Dir != tk.Repos["backend"].Worktree || !slices.Equal(git.Command, []string{"lazygit"}) || git.Transient {
		t.Fatalf("_git launched as %+v", git)
	}
	if notes.Dir != h.app.Store.TaskDir("PAY-1") || len(notes.Command) != 0 || !notes.Transient {
		t.Fatalf("_notes launched as %+v", notes)
	}

	// Saved with the session running: opened and saved.
	if err := window("frontend", "--name", "logs", "--save", "--detach", "--", "tail", "-f", "log"); err != nil {
		t.Fatal(err)
	}
	if _, ok := rt.panes["_logs"]; !ok {
		t.Fatal("_logs not opened")
	}
	if _, ok := h.load(t, "PAY-1").SavedWindow("logs"); !ok {
		t.Fatal("_logs not saved")
	}

	// Renamed, open and saved alike; the root window is not an extra one.
	if err := window("--rename", "git", "lg"); err != nil {
		t.Fatal(err)
	}
	if len(rt.renamed) != 1 || rt.renamed[0] != [2]string{"_git", "_lg"} {
		t.Fatalf("renamed %v", rt.renamed)
	}
	if _, ok := h.load(t, "PAY-1").SavedWindow("lg"); !ok {
		t.Fatal("saved window not renamed")
	}
	if err := window("--rename", "root", "x"); err == nil {
		t.Fatal("renamed the root window")
	}
	if err := window("--rename", "lg", "logs"); err == nil {
		t.Fatal("renamed onto a taken name")
	}

	// Forgotten: no longer saved, and left open.
	if err := window("--forget", "_lg"); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.load(t, "PAY-1").SavedWindow("lg"); ok {
		t.Fatal("still saved")
	}
	if _, ok := rt.panes["_lg"]; !ok {
		t.Fatal("forget closed the window")
	}
	if err := window("--forget", "nope"); err == nil || !strings.Contains(err.Error(), "saved: _notes, _logs") {
		t.Fatalf("forget of an unknown window = %v", err)
	}

	// Shown, and in JSON.
	if err := h.exec(t, h.app.newTaskCmd(), "show", "PAY-1"); err != nil || !strings.Contains(h.out.String(), "_logs in frontend: tail -f log") {
		t.Fatalf("show = %v\n%s", err, h.out.String())
	}
	if err := h.exec(t, h.app.newTaskCmd(), "show", "PAY-1", "--json"); err != nil {
		t.Fatal(err)
	}
	var shown struct {
		Windows []struct {
			Name, Repo string
		}
	}
	if err := json.Unmarshal(h.out.Bytes(), &shown); err != nil || len(shown.Windows) != 2 || shown.Windows[1].Name != "_logs" {
		t.Fatalf("json windows %+v (%v)", shown.Windows, err)
	}

	// A recipe carries them to new tasks.
	if err := h.exec(t, h.app.newProjectCmd(), "save", "recipe", "--from", "PAY-1"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-2", "--project", "recipe"); err != nil {
		t.Fatal(err)
	}
	if got := h.load(t, "PAY-2").Windows; len(got) != 2 {
		t.Fatalf("PAY-2 windows %+v", got)
	}

	// Removing a repository forgets the windows that opened in it.
	if err := h.exec(t, h.app.newRepoCmd(), "rm", "PAY-2", "frontend"); err != nil {
		t.Fatal(err)
	}
	if got := h.load(t, "PAY-2").Windows; len(got) != 1 || got[0].Name != "notes" || !strings.Contains(h.errb.String(), "forgot saved window _logs") {
		t.Fatalf("after repo rm: %+v; stderr %s", got, h.errb.String())
	}
}
