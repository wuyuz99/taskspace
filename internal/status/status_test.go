package status

import (
	"context"
	"errors"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"

	tsexec "github.com/wuyuz99/taskspace/internal/exec"
	tsgit "github.com/wuyuz99/taskspace/internal/git"
	"github.com/wuyuz99/taskspace/internal/runtime"
	"github.com/wuyuz99/taskspace/internal/task"
)

// fakeRuntime lets status tests describe a session without needing tmux.
type fakeRuntime struct {
	state runtime.SessionState
	err   error
}

func (f *fakeRuntime) Available(context.Context) error { return nil }
func (f *fakeRuntime) SessionName(t string) string     { return "tspace-" + t }
func (f *fakeRuntime) WindowName(repo string) string   { return repo }
func (f *fakeRuntime) HasSession(context.Context, string) (bool, error) {
	return f.state.Exists, nil
}
func (f *fakeRuntime) Start(context.Context, string, []runtime.Window) error { return nil }
func (f *fakeRuntime) Restart(context.Context, string, runtime.Window) error { return nil }
func (f *fakeRuntime) OpenWindow(context.Context, string, runtime.Window) error {
	return nil
}
func (f *fakeRuntime) Stop(context.Context, string) error           { return nil }
func (f *fakeRuntime) Select(context.Context, string, string) error { return nil }
func (f *fakeRuntime) Attach(context.Context, string) error         { return nil }
func (f *fakeRuntime) Focus(context.Context, string, string) (bool, error) {
	return false, nil
}
func (f *fakeRuntime) State(context.Context, string) (runtime.SessionState, error) {
	return f.state, f.err
}

func session(panes map[string]runtime.PaneState) *fakeRuntime {
	return &fakeRuntime{state: runtime.SessionState{Name: "tspace-t", Exists: true, Panes: panes}}
}

func noSession() *fakeRuntime {
	return &fakeRuntime{state: runtime.SessionState{Panes: map[string]runtime.PaneState{}}}
}

type alternateNamingRuntime struct{ *fakeRuntime }

func (f *alternateNamingRuntime) WindowName(repo string) string { return "custom/" + repo }

func TestStatusUsesBackendWindowIdentity(t *testing.T) {
	tk := task.New("t")
	tk.SetRepo("api.v1", task.Repo{Worktree: filepath.Join(t.TempDir(), "missing")})
	rt := &alternateNamingRuntime{session(map[string]runtime.PaneState{"custom/api.v1": {Dead: false}})}
	got := New(tsgit.New(tsexec.Real()), rt).Task(context.Background(), tk)
	if got.Repos[0].Runtime != RuntimeRunning {
		t.Fatalf("backend naming ignored: %+v", got)
	}
}

func initRepo(t *testing.T) string {
	t.Helper()
	if _, err := osexec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	run := func(args ...string) {
		cmd := osexec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=tspace", "GIT_AUTHOR_EMAIL=t@example.invalid",
			"GIT_COMMITTER_NAME=tspace", "GIT_COMMITTER_EMAIL=t@example.invalid")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "-b", "main", ".")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "README.md")
	run("commit", "-m", "initial")
	return dir
}

// The headline property of the design: runtime and git are independent axes,
// and "dirty while running" is the normal state of a working agent.
func TestDirtyAndRunningAreReportedIndependently(t *testing.T) {
	repo := initRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "wip.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	tk := task.New("t")
	tk.SetRepo("backend", task.Repo{Worktree: repo, Branch: "main", Agent: []string{"claude"}})

	a := New(tsgit.New(tsexec.Real()), session(map[string]runtime.PaneState{
		"backend": {Window: "backend", Dead: false},
	}))
	got := a.Task(context.Background(), tk)

	if len(got.Repos) != 1 {
		t.Fatalf("Repos = %+v, want 1", got.Repos)
	}
	row := got.Repos[0]
	if row.Runtime != RuntimeRunning {
		t.Errorf("Runtime = %q, want running", row.Runtime)
	}
	if row.Git != GitDirty {
		t.Errorf("Git = %q, want dirty", row.Git)
	}
	if !got.Running {
		t.Error("TaskStatus.Running = false, want true")
	}
}

func TestRuntimeStates(t *testing.T) {
	repo := initRepo(t)
	tk := task.New("t")
	tk.SetRepo("backend", task.Repo{Worktree: repo, Branch: "main"})

	cases := []struct {
		name string
		rt   runtime.Runtime
		want RuntimeState
	}{
		{"no session", noSession(), RuntimeNoSession},
		{"running", session(map[string]runtime.PaneState{"backend": {Dead: false}}), RuntimeRunning},
		{"dead", session(map[string]runtime.PaneState{"backend": {Dead: true}}), RuntimeDead},
		{"session without our window", session(map[string]runtime.PaneState{"other": {}}), RuntimeNoWindow},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := New(tsgit.New(tsexec.Real()), c.rt)
			got := a.Task(context.Background(), tk)
			if got.Repos[0].Runtime != c.want {
				t.Errorf("Runtime = %q, want %q", got.Repos[0].Runtime, c.want)
			}
		})
	}
}

func TestMissingWorktreeIsReportedNotFatal(t *testing.T) {
	tk := task.New("t")
	tk.SetRepo("backend", task.Repo{
		Worktree: filepath.Join(t.TempDir(), "deleted-by-hand"),
		Branch:   "agent/t",
	})

	a := New(tsgit.New(tsexec.Real()), noSession())
	got := a.Task(context.Background(), tk)

	if got.Repos[0].Git != GitMissing {
		t.Errorf("Git = %q, want missing", got.Repos[0].Git)
	}
}

func TestRowsAreOrderedByRepoName(t *testing.T) {
	repo := initRepo(t)
	tk := task.New("t")
	for _, n := range []string{"infra", "backend", "frontend"} {
		tk.SetRepo(n, task.Repo{Worktree: repo, Branch: "main"})
	}

	a := New(tsgit.New(tsexec.Real()), noSession())
	got := a.Task(context.Background(), tk)

	var names []string
	for _, r := range got.Repos {
		names = append(names, r.Repo)
	}
	if strings.Join(names, ",") != "backend,frontend,infra" {
		t.Errorf("repo order = %v, want sorted", names)
	}
}

func TestAllSortsTasks(t *testing.T) {
	repo := initRepo(t)
	var tasks []*task.Task
	for _, n := range []string{"zeta", "alpha"} {
		tk := task.New(n)
		tk.SetRepo("backend", task.Repo{Worktree: repo, Branch: "main"})
		tasks = append(tasks, tk)
	}

	a := New(tsgit.New(tsexec.Real()), noSession())
	got := a.All(context.Background(), tasks)

	if len(got) != 2 || got[0].Task != "alpha" || got[1].Task != "zeta" {
		t.Errorf("All = %+v, want alpha then zeta", got)
	}
}

func TestAgentLabelFlowsThrough(t *testing.T) {
	repo := initRepo(t)
	tk := task.New("t")
	tk.SetRepo("backend", task.Repo{Worktree: repo, Branch: "main", Agent: []string{"codex"}})
	tk.SetRepo("infra", task.Repo{Worktree: repo, Branch: "main"})

	a := New(tsgit.New(tsexec.Real()), noSession())
	got := a.Task(context.Background(), tk)

	if got.Repos[0].Agent != "codex" {
		t.Errorf("Agent = %q, want codex", got.Repos[0].Agent)
	}
	if got.Repos[1].Agent != "shell" {
		t.Errorf("Agent = %q, want shell for an agent-less repo", got.Repos[1].Agent)
	}
}

// A runtime that cannot be queried must not be reported as "nothing running":
// that would tell the user their agents are stopped when we simply do not know.
func TestUnqueryableRuntimeIsUnknownNotNoSession(t *testing.T) {
	repo := initRepo(t)
	tk := task.New("t")
	tk.SetRepo("backend", task.Repo{Worktree: repo, Branch: "main"})

	broken := &fakeRuntime{
		state: runtime.SessionState{Panes: map[string]runtime.PaneState{}},
		err:   errors.New("no server running on /tmp/tmux-1000/default"),
	}
	got := New(tsgit.New(tsexec.Real()), broken).Task(context.Background(), tk)

	if got.Repos[0].Runtime != RuntimeUnknown {
		t.Errorf("Runtime = %q, want %q", got.Repos[0].Runtime, RuntimeUnknown)
	}
	if got.RuntimeErr == nil {
		t.Error("RuntimeErr = nil; the reason must be available to explain the cell")
	}
	if got.Running {
		t.Error("Running = true despite the runtime being unqueryable")
	}
	// Git state is independent and must still be reported.
	if got.Repos[0].Git != GitClean {
		t.Errorf("Git = %q, want clean; a runtime failure must not hide git state", got.Repos[0].Git)
	}
}

// The root window is judged by its own pane, never by a repository's.
func TestRootWindowState(t *testing.T) {
	tk := task.New("t")
	tk.Agent = []string{"/usr/local/bin/claude", "--model", "opus"}
	tk.SetRepo("backend", task.Repo{Worktree: filepath.Join(t.TempDir(), "missing")})

	cases := []struct {
		name string
		rt   runtime.Runtime
		want RuntimeState
	}{
		{"no session", noSession(), RuntimeNoSession},
		{"running", session(map[string]runtime.PaneState{task.RootWindowName: {}, "backend": {Dead: true}}), RuntimeRunning},
		{"dead", session(map[string]runtime.PaneState{task.RootWindowName: {Dead: true}, "backend": {}}), RuntimeDead},
		{"session without it", session(map[string]runtime.PaneState{"backend": {}}), RuntimeNoWindow},
		{"unqueryable", &fakeRuntime{err: errors.New("no server")}, RuntimeUnknown},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := New(tsgit.New(tsexec.Real()), c.rt).Task(context.Background(), tk)
			if got.Root.Runtime != c.want {
				t.Errorf("Root.Runtime = %q, want %q", got.Root.Runtime, c.want)
			}
			if got.Root.Agent != "claude" {
				t.Errorf("Root.Agent = %q, want claude", got.Root.Agent)
			}
		})
	}

	tk.Agent = nil
	if got := New(tsgit.New(tsexec.Real()), noSession()).Task(context.Background(), tk); got.Root.Agent != "shell" {
		t.Errorf("Root.Agent = %q without an agent, want shell", got.Root.Agent)
	}
}
