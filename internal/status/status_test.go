package status

import (
	"context"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"

	awexec "github.com/aw-tool/aw/internal/exec"
	awgit "github.com/aw-tool/aw/internal/git"
	"github.com/aw-tool/aw/internal/runtime"
	"github.com/aw-tool/aw/internal/task"
)

// fakeRuntime lets status tests describe a session without needing tmux.
type fakeRuntime struct {
	state runtime.SessionState
	err   error
}

func (f *fakeRuntime) Name() string                    { return "fake" }
func (f *fakeRuntime) Available(context.Context) error { return nil }
func (f *fakeRuntime) SessionName(t string) string     { return "aw-" + t }
func (f *fakeRuntime) HasSession(context.Context, string) (bool, error) {
	return f.state.Exists, nil
}
func (f *fakeRuntime) Start(context.Context, string, []runtime.Window) error { return nil }
func (f *fakeRuntime) OpenWindow(context.Context, string, runtime.Window) error {
	return nil
}
func (f *fakeRuntime) Stop(context.Context, string) error   { return nil }
func (f *fakeRuntime) Attach(context.Context, string) error { return nil }
func (f *fakeRuntime) State(context.Context, string) (runtime.SessionState, error) {
	return f.state, f.err
}

func session(panes map[string]runtime.PaneState) *fakeRuntime {
	return &fakeRuntime{state: runtime.SessionState{Name: "aw-t", Exists: true, Panes: panes}}
}

func noSession() *fakeRuntime {
	return &fakeRuntime{state: runtime.SessionState{Panes: map[string]runtime.PaneState{}}}
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
			"GIT_AUTHOR_NAME=aw", "GIT_AUTHOR_EMAIL=t@example.invalid",
			"GIT_COMMITTER_NAME=aw", "GIT_COMMITTER_EMAIL=t@example.invalid")
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

	a := New(awgit.New(awexec.Real()), session(map[string]runtime.PaneState{
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
			a := New(awgit.New(awexec.Real()), c.rt)
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

	a := New(awgit.New(awexec.Real()), noSession())
	got := a.Task(context.Background(), tk)

	if got.Repos[0].Git != GitMissing {
		t.Errorf("Git = %q, want missing", got.Repos[0].Git)
	}
	if got.Repos[0].GitSummary() != "missing" {
		t.Errorf("GitSummary = %q, want missing", got.Repos[0].GitSummary())
	}
}

func TestRowsAreOrderedByRepoName(t *testing.T) {
	repo := initRepo(t)
	tk := task.New("t")
	for _, n := range []string{"infra", "backend", "frontend"} {
		tk.SetRepo(n, task.Repo{Worktree: repo, Branch: "main"})
	}

	a := New(awgit.New(awexec.Real()), noSession())
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

	a := New(awgit.New(awexec.Real()), noSession())
	got := a.All(context.Background(), tasks)

	if len(got) != 2 || got[0].Task != "alpha" || got[1].Task != "zeta" {
		t.Errorf("All = %+v, want alpha then zeta", got)
	}
}

func TestGitSummary(t *testing.T) {
	cases := []struct {
		name string
		row  RepoStatus
		want string
	}{
		{"clean", RepoStatus{Git: GitClean}, "clean"},
		{"missing", RepoStatus{Git: GitMissing}, "missing"},
		{"error", RepoStatus{Git: GitError}, "error"},
		{
			"one dirty file",
			RepoStatus{Git: GitDirty, Detail: awgit.Status{Modified: 1}},
			"dirty (1 file)",
		},
		{
			"several dirty files",
			RepoStatus{Git: GitDirty, Detail: awgit.Status{Modified: 2, Untracked: 1}},
			"dirty (3 files)",
		},
		{
			"clean but ahead",
			RepoStatus{Git: GitClean, Detail: awgit.Status{Ahead: 2}},
			"clean, ahead 2",
		},
		{
			"stash only",
			RepoStatus{Git: GitDirty, Detail: awgit.Status{Stashes: 1}},
			"clean, 1 stash",
		},
		{
			"everything at once",
			RepoStatus{Git: GitDirty, Detail: awgit.Status{Modified: 1, Stashes: 2, Ahead: 1, Behind: 3}},
			"dirty (1 file), 2 stashes, ahead 1, behind 3",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.row.GitSummary(); got != c.want {
				t.Errorf("GitSummary() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestAgentLabelFlowsThrough(t *testing.T) {
	repo := initRepo(t)
	tk := task.New("t")
	tk.SetRepo("backend", task.Repo{Worktree: repo, Branch: "main", Agent: []string{"codex"}})
	tk.SetRepo("infra", task.Repo{Worktree: repo, Branch: "main"})

	a := New(awgit.New(awexec.Real()), noSession())
	got := a.Task(context.Background(), tk)

	if got.Repos[0].Agent != "codex" {
		t.Errorf("Agent = %q, want codex", got.Repos[0].Agent)
	}
	if got.Repos[1].Agent != "shell" {
		t.Errorf("Agent = %q, want shell for an agent-less repo", got.Repos[1].Agent)
	}
}
