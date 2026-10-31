package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tsgit "github.com/wuyuz99/taskspace/internal/git"
	"github.com/wuyuz99/taskspace/internal/runtime"
	"github.com/wuyuz99/taskspace/internal/task"
)

// moveFixture is renameFixture's task OLD, with repositories a and b, and a
// started task NEW with repository c.
func moveFixture(t *testing.T) (f *fixture, old, dst *task.Task) {
	t.Helper()
	f, old = renameFixture(t)
	dst = f.task(t, "NEW", map[string]task.Repo{"c": {Path: gitRepo(t)}})
	if _, err := f.svc.Start(t.Context(), "NEW", StartOptions{NoRuntime: true}); err != nil {
		t.Fatal(err)
	}
	f.events = nil
	return f, old, dst
}

func saveTask(t *testing.T, f *fixture, tk *task.Task) {
	t.Helper()
	if err := f.svc.Store.Save(tk); err != nil {
		t.Fatal(err)
	}
}

// assertNotMoved checks both records are as given and a is where it was.
func assertNotMoved(t *testing.T, f *fixture, old, dst *task.Task) {
	t.Helper()
	for _, want := range []*task.Task{old, dst} {
		got, err := f.svc.Store.Load(want.Name)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("record = %+v, %v; want %+v", got, err, want)
		}
	}
	if branch := renameGit(t, old.Repos["a"].Worktree, "branch", "--show-current"); branch != old.Repos["a"].Branch {
		t.Fatalf("worktree on %s", branch)
	}
	if _, err := os.Stat(f.svc.Store.WorktreePath("NEW", "a")); !os.IsNotExist(err) {
		t.Fatalf("destination: %v", err)
	}
}

func TestMoveRepoKeepsItsWorkAndSettings(t *testing.T) {
	for _, name := range []string{"", "renamed"} {
		t.Run("name="+name, func(t *testing.T) {
			f, old, _ := moveFixture(t)
			r := old.Repos["a"]
			r.BaseAssumed, r.PostCreate = true, "make setup"
			r.Agent, r.Resume = []string{"repo-agent"}, []string{"repo-agent", "--continue"}
			old.Repos["a"] = r
			old.Windows = []task.Window{{Name: "logs", Repo: "a", Command: []string{"tail"}}, {Name: "docs", Repo: "b"}, {Name: "top"}}
			saveTask(t, f, old)
			if err := os.WriteFile(filepath.Join(r.Worktree, "README.md"), []byte("unfinished\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(r.Worktree, ".env"), []byte("secret\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := f.svc.Store.MarkLaunched("OLD", "a"); err != nil {
				t.Fatal(err)
			}
			if err := f.svc.MoveRepo(t.Context(), "OLD", "a", "NEW", name); err != nil {
				t.Fatal(err)
			}
			want := "a"
			if name != "" {
				want = name
			}
			from, err := f.svc.Store.Load("OLD")
			if err != nil {
				t.Fatal(err)
			}
			got, err := f.svc.Store.Load("NEW")
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := from.Repo("a"); ok {
				t.Fatal("old task still claims it")
			}
			wantRepo := r
			wantRepo.Worktree = f.svc.Store.WorktreePath("NEW", want)
			if !reflect.DeepEqual(got.Repos[want], wantRepo) {
				t.Fatalf("repo = %+v; want %+v", got.Repos[want], wantRepo)
			}
			if !reflect.DeepEqual(got.Windows, []task.Window{{Name: "logs", Repo: want, Command: []string{"tail"}}}) {
				t.Fatalf("new windows = %+v", got.Windows)
			}
			if !reflect.DeepEqual(from.Windows, []task.Window{{Name: "docs", Repo: "b"}, {Name: "top"}}) {
				t.Fatalf("old windows = %+v", from.Windows)
			}
			for file, content := range map[string]string{"README.md": "unfinished\n", ".env": "secret\n"} {
				if b, err := os.ReadFile(filepath.Join(wantRepo.Worktree, file)); err != nil || string(b) != content {
					t.Fatalf("%s: %q, %v", file, b, err)
				}
			}
			if _, err := os.Stat(r.Worktree); !os.IsNotExist(err) {
				t.Fatalf("old worktree remains: %v", err)
			}
			if branch := renameGit(t, wantRepo.Worktree, "branch", "--show-current"); branch != r.Branch {
				t.Fatalf("branch = %s", branch)
			}
			// Its conversation stayed behind, so there is nothing to resume.
			if f.svc.Store.Launched("OLD", "a") || f.svc.Store.Launched("NEW", want) {
				t.Fatal("launch outlived the conversation")
			}
			// Both tasks work afterwards, without making anything new.
			f.events = nil
			for _, n := range []string{"OLD", "NEW"} {
				if _, err := f.svc.Start(t.Context(), n, StartOptions{NoRuntime: true}); err != nil {
					t.Fatal(err)
				}
			}
			for _, e := range eventsOf[WorktreeReady](f.events) {
				if e.Result.Created {
					t.Fatalf("recreated %s", e.Repo)
				}
			}
		})
	}
}

func TestMoveRepoWithNoWorktreeMovesTheRecord(t *testing.T) {
	f := newFixture(t)
	f.task(t, "OLD", map[string]task.Repo{"a": {Path: gitRepo(t)}})
	f.task(t, "NEW", nil)
	if err := f.svc.MoveRepo(t.Context(), "OLD", "a", "NEW", ""); err != nil {
		t.Fatal(err)
	}
	got, err := f.svc.Store.Load("NEW")
	if err != nil || got.Repos["a"].Worktree != f.svc.Store.WorktreePath("NEW", "a") {
		t.Fatalf("record = %+v, %v", got, err)
	}
	if _, err := os.Stat(f.svc.Store.TaskDir("NEW")); !os.IsNotExist(err) {
		t.Fatalf("made a task directory for nothing: %v", err)
	}
}

func TestMoveRepoRefusals(t *testing.T) {
	session := func(f *fixture, panes ...string) {
		f.rt.exists, f.rt.panes = true, map[string]runtime.PaneState{}
		for _, p := range panes {
			f.rt.panes[p] = runtime.PaneState{Window: p}
		}
	}
	for _, c := range []struct {
		kind, to, repo, name, want string
		prepare                    func(*testing.T, *fixture, *task.Task, *task.Task)
	}{
		{kind: "same-task", to: "OLD", want: "already in task OLD"},
		{kind: "no-task", to: "MISSING", want: "task not found"},
		{kind: "no-repo", repo: "z", want: `no repo named "z"`},
		{kind: "bad-name", name: "a/b", want: "path separator"},
		{kind: "name-taken", name: "c", want: `already has a repo named "c"`},
		{kind: "already-attached", want: "already attached", prepare: func(t *testing.T, f *fixture, old, dst *task.Task) {
			r := dst.Repos["c"]
			r.Path = old.Repos["a"].Path
			dst.Repos["c"] = r
			saveTask(t, f, dst)
		}},
		{kind: "saved-window-taken", want: "already has a saved window _logs", prepare: func(t *testing.T, f *fixture, old, dst *task.Task) {
			old.Windows = []task.Window{{Name: "logs", Repo: "a"}}
			dst.Windows = []task.Window{{Name: "logs"}}
			saveTask(t, f, old)
			saveTask(t, f, dst)
		}},
		{kind: "window-open", want: "tspace stop OLD a", prepare: func(t *testing.T, f *fixture, old, dst *task.Task) {
			session(f, "a", "b")
		}},
		{kind: "saved-window-open", want: "tspace stop OLD _logs", prepare: func(t *testing.T, f *fixture, old, dst *task.Task) {
			old.Windows = []task.Window{{Name: "logs", Repo: "a"}}
			saveTask(t, f, old)
			session(f, "b", "_logs")
		}},
		{kind: "unsaved-window", want: "no saved repository", prepare: func(t *testing.T, f *fixture, old, dst *task.Task) {
			session(f, "b", "_scratch")
		}},
		{kind: "agent-window", want: "stop the task first", prepare: func(t *testing.T, f *fixture, old, dst *task.Task) {
			old.AgentRepo = "a"
			saveTask(t, f, old)
			session(f, "b")
		}},
		{kind: "destination-window", want: "NEW already has a window a", prepare: func(t *testing.T, f *fixture, old, dst *task.Task) {
			f.rt.exists = true
			f.svc.Runtime = sessionPanes{fakeRuntime: f.rt, panes: map[string][]string{"tspace-OLD": {"b"}, "tspace-NEW": {"a"}}}
		}},
		{kind: "outside-its-path", want: "outside its expected path", prepare: func(t *testing.T, f *fixture, old, dst *task.Task) {
			r := old.Repos["a"]
			r.Worktree = filepath.Join(f.svc.Store.Root(), "elsewhere")
			old.Repos["a"] = r
			saveTask(t, f, old)
		}},
		{kind: "session-unknown", want: "cannot inspect", prepare: func(t *testing.T, f *fixture, old, dst *task.Task) {
			f.rt.hasErr = errors.New("cannot inspect session")
		}},
		{kind: "inside", want: "from outside", prepare: func(t *testing.T, f *fixture, old, dst *task.Task) {
			t.Chdir(old.Repos["a"].Worktree)
		}},
		{kind: "locked", want: "locked", prepare: func(t *testing.T, f *fixture, old, dst *task.Task) {
			renameGit(t, old.Repos["a"].Path, "worktree", "lock", old.Repos["a"].Worktree)
		}},
		{kind: "destination-taken", want: "already exists", prepare: func(t *testing.T, f *fixture, old, dst *task.Task) {
			if err := os.WriteFile(f.svc.Store.WorktreePath("NEW", "a"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(c.kind, func(t *testing.T) {
			f, old, dst := moveFixture(t)
			if c.prepare != nil {
				c.prepare(t, f, old, dst)
			}
			to, repo := c.to, c.repo
			if to == "" {
				to = "NEW"
			}
			if repo == "" {
				repo = "a"
			}
			err := f.svc.MoveRepo(t.Context(), "OLD", repo, to, c.name)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("move = %v; want %q", err, c.want)
			}
			if c.kind == "destination-taken" {
				os.Remove(f.svc.Store.WorktreePath("NEW", "a"))
			}
			if c.kind != "outside-its-path" {
				assertNotMoved(t, f, old, dst)
			}
		})
	}
}

// sessionPanes gives each session windows of its own.
type sessionPanes struct {
	*fakeRuntime
	panes map[string][]string
}

func (s sessionPanes) State(_ context.Context, session string) (runtime.SessionState, error) {
	st := runtime.SessionState{Exists: true, Panes: map[string]runtime.PaneState{}}
	for _, p := range s.panes[session] {
		st.Panes[p] = runtime.PaneState{Window: p}
	}
	return st, nil
}

// With its window closed, a repository leaves the running session alone.
func TestMoveRepoFromARunningTask(t *testing.T) {
	f, _, _ := moveFixture(t)
	f.rt.exists, f.rt.panes = true, map[string]runtime.PaneState{"b": {Window: "b"}}
	if err := f.svc.MoveRepo(t.Context(), "OLD", "a", "NEW", ""); err != nil {
		t.Fatal(err)
	}
	if f.rt.stopped {
		t.Fatal("stopped the session")
	}
}

func TestMoveRepoRollsBack(t *testing.T) {
	for _, kind := range []string{"git", "cancelled", "new-record", "old-record"} {
		t.Run(kind, func(t *testing.T) {
			f, old, dst := moveFixture(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			// A directory in a record's place fails the save of that record.
			record, data := "", []byte(nil)
			block := func(name string) {
				record = filepath.Join(f.svc.Store.TasksDir(), name+".json")
				var err error
				if data, err = os.ReadFile(record); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(record); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(record, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			runner := &renameFailRunner{}
			once := func(do func()) {
				runner.after = func() { runner.after = nil; do() }
			}
			switch kind {
			case "git":
				runner.fail = 1
			case "cancelled":
				once(cancel)
			case "new-record":
				once(func() { block("NEW") })
			case "old-record":
				once(func() { block("OLD") })
			}
			f.svc.Git = tsgit.New(runner)
			err := f.svc.MoveRepo(ctx, "OLD", "a", "NEW", "")
			if err == nil || strings.Contains(err.Error(), "rollback:") {
				t.Fatalf("move = %v", err)
			}
			if record != "" {
				if err := os.Remove(record); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(record, data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			assertNotMoved(t, f, old, dst)
		})
	}
}

func TestMoveRepoRollbackRemovesADirectoryItMade(t *testing.T) {
	f, old := renameFixture(t)
	dst := f.task(t, "NEW", nil)
	f.svc.Git = tsgit.New(&renameFailRunner{fail: 1})
	if err := f.svc.MoveRepo(t.Context(), "OLD", "a", "NEW", ""); err == nil {
		t.Fatal("move accepted")
	}
	assertNotMoved(t, f, old, dst)
	if _, err := os.Stat(f.svc.Store.TaskDir("NEW")); !os.IsNotExist(err) {
		t.Fatalf("new root remains: %v", err)
	}
}

func TestMoveRepoSettlesBothLayouts(t *testing.T) {
	t.Run("agent repo leaves and fills an empty task", func(t *testing.T) {
		f, old := renameFixture(t)
		old.AgentRepo, old.Resume = "a", []string{"agent", "--continue"}
		saveTask(t, f, old)
		f.task(t, "NEW", nil)
		if err := f.svc.Store.MarkLaunched("OLD", "a"); err != nil {
			t.Fatal(err)
		}
		if err := f.svc.MoveRepo(t.Context(), "OLD", "a", "NEW", ""); err != nil {
			t.Fatal(err)
		}
		from, _ := f.svc.Store.Load("OLD")
		got, _ := f.svc.Store.Load("NEW")
		if from.AgentRepo != "" || !from.RootWindow {
			t.Fatalf("old layout: %+v", from)
		}
		if got.AgentRepo != "a" {
			t.Fatalf("new layout: %+v", got)
		}
		// That agent is OLD's: NEW's has not run, so it has nothing to resume.
		if f.svc.Store.Launched("NEW", "a") || f.svc.Store.Launched("OLD", "a") {
			t.Fatal("launch survived the agent")
		}
		var notes []string
		for _, w := range eventsOf[Warning](f.events) {
			notes = append(notes, w.Text)
		}
		text := strings.Join(notes, "\n")
		if !strings.Contains(text, "moves to the root window") || !strings.Contains(text, "conversation from "+old.Repos["a"].Worktree) {
			t.Fatalf("notes:\n%s", text)
		}
	})
	t.Run("second repository gives a root window", func(t *testing.T) {
		f, _, dst := moveFixture(t)
		dst.AgentRepo = "c"
		saveTask(t, f, dst)
		if err := f.svc.MoveRepo(t.Context(), "OLD", "a", "NEW", ""); err != nil {
			t.Fatal(err)
		}
		got, _ := f.svc.Store.Load("NEW")
		if got.AgentRepo != "c" || !got.RootWindow {
			t.Fatalf("layout: %+v", got)
		}
		if len(eventsOf[RootWindowPending](f.events)) != 1 {
			t.Fatalf("events: %+v", f.events)
		}
	})
	t.Run("the task's agent stays with its task", func(t *testing.T) {
		f, old, _ := moveFixture(t)
		old.AgentRepo = "a"
		saveTask(t, f, old)
		if err := f.svc.MoveRepo(t.Context(), "OLD", "a", "NEW", ""); err != nil {
			t.Fatal(err)
		}
		got, _ := f.svc.Store.Load("NEW")
		if len(got.RepoAgent("a")) != 0 {
			t.Fatalf("agent: %+v", got.RepoAgent("a"))
		}
		for _, w := range eventsOf[Warning](f.events) {
			if strings.Contains(w.Text, "stays with that task") {
				return
			}
		}
		t.Fatalf("no note: %+v", f.events)
	})
}

func TestMoveRepoCarriesConversations(t *testing.T) {
	c := &fakeCarrier{has: map[string]bool{}}
	f, tk := conversationFixture(t, c, true)
	f.task(t, "NEW", nil)
	wt := tk.Repos["a"].Worktree
	c.has[wt] = true
	if err := f.svc.MoveRepo(t.Context(), "OLD", "a", "NEW", ""); err != nil {
		t.Fatal(err)
	}
	dest := f.svc.Store.WorktreePath("NEW", "a")
	if !c.has[dest] {
		t.Fatalf("carried: %+v", c.calls)
	}
	if !f.svc.Store.Launched("NEW", "a") {
		t.Fatal("a carried conversation is not resumed")
	}
	if carried := eventsOf[ConversationCarried](f.events); len(carried) != 1 || carried[0].To != dest {
		t.Fatalf("events: %+v", carried)
	}
	for _, w := range eventsOf[Warning](f.events) {
		if strings.Contains(w.Text, "conversation from") {
			t.Fatalf("noted a carried conversation: %s", w.Text)
		}
	}
}

// A worktree that never existed moved nowhere, so its history stays put.
func TestMoveRepoCarriesNothingWithoutAWorktree(t *testing.T) {
	f := newFixture(t)
	tk := f.task(t, "OLD", map[string]task.Repo{"a": {Path: gitRepo(t)}})
	f.task(t, "NEW", nil)
	c := &fakeCarrier{has: map[string]bool{tk.Repos["a"].Worktree: true}}
	f.svc.Carriers = map[string]ConversationCarrier{"fake": c}
	if err := f.svc.Store.SaveConfig(&task.Config{CarryConversations: []string{"fake"}}); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.MoveRepo(t.Context(), "OLD", "a", "NEW", ""); err != nil {
		t.Fatal(err)
	}
	if len(c.calls) != 0 {
		t.Fatalf("carried: %+v", c.calls)
	}
}

func TestMoveRepoNotesConversationLeftBehind(t *testing.T) {
	f, tk := conversationFixture(t, &fakeCarrier{has: map[string]bool{}}, false)
	f.task(t, "NEW", nil)
	if err := f.svc.MoveRepo(t.Context(), "OLD", "a", "NEW", ""); err != nil {
		t.Fatal(err)
	}
	var notes int
	for _, w := range eventsOf[Warning](f.events) {
		if strings.Contains(w.Text, "conversation from "+tk.Repos["a"].Worktree) {
			notes++
		}
	}
	if notes != 1 {
		t.Fatalf("notes: %+v", eventsOf[Warning](f.events))
	}
}
