package cli

import (
	"bytes"
	"context"
	"os"
	osexec "os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	awexec "github.com/wuyuz99/taskspace/internal/exec"
	awgit "github.com/wuyuz99/taskspace/internal/git"
	"github.com/wuyuz99/taskspace/internal/runtime"
	"github.com/wuyuz99/taskspace/internal/task"
)

// stubRuntime stands in for tmux so the safety-critical CLI paths can be tested
// on any machine.
type stubRuntime struct{ stopped bool }

func (s *stubRuntime) Available(context.Context) error { return nil }
func (s *stubRuntime) SessionName(t string) string     { return "aw-" + t }
func (s *stubRuntime) WindowName(repo string) string   { return repo }
func (s *stubRuntime) HasSession(context.Context, string) (bool, error) {
	return false, nil
}
func (s *stubRuntime) Start(context.Context, string, []runtime.Window) error    { return nil }
func (s *stubRuntime) OpenWindow(context.Context, string, runtime.Window) error { return nil }
func (s *stubRuntime) Restart(context.Context, string, runtime.Window) error    { return nil }
func (s *stubRuntime) Stop(context.Context, string) error {
	s.stopped = true
	return nil
}
func (s *stubRuntime) Attach(context.Context, string) error         { return nil }
func (s *stubRuntime) Select(context.Context, string, string) error { return nil }
func (s *stubRuntime) Focus(context.Context, string, string) (bool, error) {
	return false, nil
}
func (s *stubRuntime) State(context.Context, string) (runtime.SessionState, error) {
	return runtime.SessionState{Panes: map[string]runtime.PaneState{}}, nil
}

type harness struct {
	app  *App
	out  *bytes.Buffer
	errb *bytes.Buffer
	repo string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	if _, err := osexec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	root := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	out, errb := &bytes.Buffer{}, &bytes.Buffer{}

	return &harness{
		app: &App{
			Store:   task.NewStore(root),
			Git:     awgit.New(awexec.Real()),
			Runtime: &stubRuntime{},
			Out:     out,
			Err:     errb,
		},
		out:  out,
		errb: errb,
		repo: mkRepo(t),
	}
}

func mkRepo(t *testing.T) string {
	t.Helper()
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
		if o, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, o)
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

// exec runs one aw command through cobra, as the binary would.
func (h *harness) exec(t *testing.T, cmd *cobra.Command, args ...string) error {
	t.Helper()
	h.out.Reset()
	h.errb.Reset()
	cmd.SetArgs(args)
	cmd.SetOut(h.out)
	cmd.SetErr(h.errb)
	return cmd.ExecuteContext(context.Background())
}

// startedTask builds a task with one repo and a real worktree.
func (h *harness) startedTask(t *testing.T, name string) *task.Task {
	t.Helper()
	if _, err := h.app.Store.Create(name); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := h.exec(t, h.app.newRepoCmd(), "add", name, h.repo); err != nil {
		t.Fatalf("repo add: %v", err)
	}
	if err := h.exec(t, h.app.newStartCmd(), name, "--no-runtime"); err != nil {
		t.Fatalf("start: %v", err)
	}
	tk, err := h.app.Store.Load(name)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return tk
}

func TestCleanRefusesDirtyWorktree(t *testing.T) {
	h := newHarness(t)
	tk := h.startedTask(t, "dirty-task")
	wt := tk.Repos["backend"].Worktree
	if wt == "" {
		for _, n := range tk.RepoNames() {
			wt = tk.Repos[n].Worktree
		}
	}
	if err := os.WriteFile(filepath.Join(wt, "wip.txt"), []byte("unsaved"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := h.exec(t, h.app.newCleanCmd(), "dirty-task")
	if err == nil {
		t.Fatal("clean on a dirty worktree = nil, want refusal")
	}
	if !strings.Contains(err.Error(), "refusing to clean") {
		t.Errorf("error = %v, want a refusal", err)
	}
	// The refusal must be actionable: say what is in the way and how to proceed.
	if !strings.Contains(err.Error(), "uncommitted change") {
		t.Errorf("error = %v, want it to name the uncommitted changes", err)
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Errorf("error = %v, want it to mention --force", err)
	}
	if _, statErr := os.Stat(filepath.Join(wt, "wip.txt")); statErr != nil {
		t.Error("a refused clean deleted the work anyway")
	}
}

func TestCleanRefusesOnAStashAlone(t *testing.T) {
	h := newHarness(t)
	tk := h.startedTask(t, "stash-task")
	var wt, branch string
	for _, n := range tk.RepoNames() {
		wt, branch = tk.Repos[n].Worktree, tk.Repos[n].Branch
	}

	if err := os.WriteFile(filepath.Join(wt, "README.md"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stash := osexec.Command("git", "stash", "push", "-m", "wip")
	stash.Dir = wt
	stash.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=aw", "GIT_AUTHOR_EMAIL=t@example.invalid",
		"GIT_COMMITTER_NAME=aw", "GIT_COMMITTER_EMAIL=t@example.invalid")
	if o, err := stash.CombinedOutput(); err != nil {
		t.Fatalf("git stash: %v\n%s", err, o)
	}

	// The tree is now clean; only the stash stands between clean and data loss.
	err := h.exec(t, h.app.newCleanCmd(), "stash-task")
	if err == nil {
		t.Fatalf("clean with a stash on %s = nil, want refusal", branch)
	}
	if !strings.Contains(err.Error(), "stash") {
		t.Errorf("error = %v, want it to mention the stash", err)
	}
}

func TestCleanSucceedsOnCleanWorktreeAndKeepsBranch(t *testing.T) {
	h := newHarness(t)
	tk := h.startedTask(t, "clean-task")
	var wt, branch, repoPath string
	for _, n := range tk.RepoNames() {
		wt, branch, repoPath = tk.Repos[n].Worktree, tk.Repos[n].Branch, tk.Repos[n].Path
	}

	if err := h.exec(t, h.app.newCleanCmd(), "clean-task"); err != nil {
		t.Fatalf("clean: %v", err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Error("worktree still present after clean")
	}
	// Branches are not deleted by default.
	g := awgit.New(awexec.Real())
	if ok, err := g.BranchExists(context.Background(), repoPath, branch); err != nil || !ok {
		t.Errorf("BranchExists(%s) = %v, %v; clean must not delete branches", branch, ok, err)
	}
	// The task record survives clean.
	if !h.app.Store.Exists("clean-task") {
		t.Error("clean deleted the task record; that is aw task rm's job")
	}
}

func TestTaskRmRefusesWhileWorktreesExist(t *testing.T) {
	h := newHarness(t)
	h.startedTask(t, "live-task")

	err := h.exec(t, h.app.newTaskCmd(), "rm", "live-task")
	if err == nil {
		t.Fatal("task rm with live worktrees = nil, want refusal")
	}
	if !strings.Contains(err.Error(), "aw clean") {
		t.Errorf("error = %v, want it to point at aw clean", err)
	}
	if !h.app.Store.Exists("live-task") {
		t.Error("refused task rm deleted the record anyway")
	}
}

func TestRepoAddRejectsNonRepository(t *testing.T) {
	h := newHarness(t)
	if _, err := h.app.Store.Create("t"); err != nil {
		t.Fatal(err)
	}
	err := h.exec(t, h.app.newRepoCmd(), "add", "t", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "not a git repository") {
		t.Fatalf("error = %v, want 'not a git repository'", err)
	}
}

func TestRepoAddRejectsDuplicatePath(t *testing.T) {
	h := newHarness(t)
	if _, err := h.app.Store.Create("t"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newRepoCmd(), "add", "t", h.repo); err != nil {
		t.Fatalf("first add: %v", err)
	}
	err := h.exec(t, h.app.newRepoCmd(), "add", "t", h.repo, "--name", "other")
	if err == nil || !strings.Contains(err.Error(), "already attached") {
		t.Fatalf("error = %v, want 'already attached'", err)
	}
}

func TestStartRecordsResolvedBase(t *testing.T) {
	h := newHarness(t)
	tk := h.startedTask(t, "base-task")
	for _, n := range tk.RepoNames() {
		if got := tk.Repos[n].Base; got != "main" {
			t.Errorf("Base = %q, want main recorded at add time", got)
		}
	}
}

func TestPathPrintsOnlyThePath(t *testing.T) {
	h := newHarness(t)
	tk := h.startedTask(t, "path-task")
	var want string
	for _, n := range tk.RepoNames() {
		want = tk.Repos[n].Worktree
	}

	if err := h.exec(t, h.app.newPathCmd(), "path-task"); err != nil {
		t.Fatalf("path: %v", err)
	}
	if got := strings.TrimSpace(h.out.String()); got != want {
		t.Errorf("path printed %q, want exactly %q", got, want)
	}
}

// The hook must be able to reach its source repository without a relative path
// out of the worktree: that depth depends on where AW_ROOT is, so such a hook
// silently breaks for anyone who moves their state directory.
func TestPostCreateHookGetsRepoPathInEnvironment(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("Unix shell test")
	}
	h := newHarness(t)
	if _, err := h.app.Store.Create("hook-task"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newRepoCmd(), "add", "hook-task", h.repo,
		"--post-create", `printf '%s\n' "$AW_REPO_PATH" > hook-saw.txt`); err != nil {
		t.Fatalf("repo add: %v", err)
	}
	if err := h.exec(t, h.app.newStartCmd(), "hook-task", "--no-runtime"); err != nil {
		t.Fatalf("start: %v", err)
	}

	tk, err := h.app.Store.Load("hook-task")
	if err != nil {
		t.Fatal(err)
	}
	var wt string
	for _, n := range tk.RepoNames() {
		wt = tk.Repos[n].Worktree
	}

	got, err := os.ReadFile(filepath.Join(wt, "hook-saw.txt"))
	if err != nil {
		t.Fatalf("hook did not run: %v", err)
	}
	if strings.TrimSpace(string(got)) != h.repo {
		t.Errorf("AW_REPO_PATH = %q, want %q", strings.TrimSpace(string(got)), h.repo)
	}
}
