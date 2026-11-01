package workspace

import (
	"bytes"
	"context"
	"errors"
	"os"
	osexec "os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tsexec "github.com/wuyuz99/taskspace/internal/exec"
	tsgit "github.com/wuyuz99/taskspace/internal/git"
	"github.com/wuyuz99/taskspace/internal/runtime"
	"github.com/wuyuz99/taskspace/internal/runtime/runtimetest"
	"github.com/wuyuz99/taskspace/internal/task"
)

type fixture struct {
	svc    *Service
	rt     *runtimetest.Fake
	events []Event
	hooks  bytes.Buffer
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	if _, err := osexec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	t.Setenv(EnvTask, "")
	t.Setenv(EnvWindow, "")
	root := t.TempDir()
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	f := &fixture{rt: runtimetest.New()}
	f.svc = &Service{
		Store: task.NewStore(root), Git: tsgit.New(tsexec.Real()), Runtime: f.rt,
		Stdout: &f.hooks, Stderr: &f.hooks,
		Progress: func(e Event) { f.events = append(f.events, e) },
	}
	return f
}

func (f *fixture) windows(op runtimetest.Op) []runtime.Window {
	var out []runtime.Window
	for _, c := range f.rt.Calls(op) {
		out = append(out, c.Window)
	}
	return out
}

func (f *fixture) started() []runtime.Window   { return f.windows(runtimetest.OpStart) }
func (f *fixture) restarted() []runtime.Window { return f.windows(runtimetest.OpRestart) }
func (f *fixture) stopped() bool               { return len(f.rt.Calls(runtimetest.OpStop)) > 0 }

// dead leaves window of task name open with its command exited.
func (f *fixture) dead(name, window string) { f.rt.Exit(f.rt.SessionName(name), window) }

func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		dir = r
	}
	run := func(args ...string) {
		cmd := osexec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
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

// task makes a task with the given repositories, each on a new branch.
func (f *fixture) task(t *testing.T, name string, repos map[string]task.Repo) *task.Task {
	t.Helper()
	tk := task.New(name)
	tk.Agent = []string{"agent", "--notes", "{context_dir}"}
	for n, r := range repos {
		r.Branch = "agent/" + name
		r.Base = "main"
		r.Worktree = f.svc.Store.WorktreePath(name, n)
		tk.SetRepo(n, r)
	}
	if err := f.svc.Store.CreatePrepared(tk); err != nil {
		t.Fatal(err)
	}
	return tk
}

func eventsOf[E Event](events []Event) []E {
	var out []E
	for _, e := range events {
		if x, ok := e.(E); ok {
			out = append(out, x)
		}
	}
	return out
}

func TestStartRefusesReplacedWorktree(t *testing.T) {
	f := newFixture(t)
	tk := f.task(t, "T", map[string]task.Repo{"a": {Path: gitRepo(t)}})
	ctx := context.Background()
	if _, err := f.svc.Start(ctx, "T", StartOptions{NoRuntime: true}); err != nil {
		t.Fatal(err)
	}
	wt := tk.Repos["a"].Worktree
	if err := os.Rename(wt, wt+"-saved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, noRuntime := range []bool{true, false} {
		f.events = nil
		if _, err := f.svc.Start(ctx, "T", StartOptions{NoRuntime: noRuntime}); err == nil {
			t.Fatalf("Start(NoRuntime=%v) accepted replacement", noRuntime)
		}
		if len(eventsOf[WorktreeReady](f.events)) != 0 || len(f.started()) != 0 {
			t.Fatalf("reported ready or launched runtime: %+v, %+v", f.events, f.started())
		}
	}
}

func TestStartAndRepeat(t *testing.T) {
	f := newFixture(t)
	f.task(t, "T", map[string]task.Repo{"a": {Path: gitRepo(t)}, "b": {Path: gitRepo(t)}})
	res, err := f.svc.Start(context.Background(), "T", StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Session != "tspace@T" || len(eventsOf[WorktreeReady](f.events)) != 2 {
		t.Fatalf("result %+v, events %+v", res, f.events)
	}
	names := []string{}
	for _, w := range f.started() {
		names = append(names, w.Name)
	}
	if !slices.Equal(names, []string{task.RootWindowName, "a", "b"}) {
		t.Fatalf("started %v", names)
	}
	// The root agent's placeholders are filled in.
	if root := f.started()[0]; !slices.Contains(root.Command, f.svc.Store.ContextDir("T")) {
		t.Errorf("root command %v", root.Command)
	}

	// Again: worktrees reused, no window started twice.
	f.events = nil
	if _, err := f.svc.Start(context.Background(), "T", StartOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, r := range eventsOf[WorktreeReady](f.events) {
		if r.Result.Created {
			t.Errorf("%s created again", r.Repo)
		}
	}
	if len(f.started()) != 3 {
		t.Fatalf("windows started again: %d", len(f.started()))
	}
}

// A failing hook is a warning; a window that dies at once is reported; an
// unavailable runtime fails after the worktrees, which stay.
func TestStartPartialFailures(t *testing.T) {
	f := newFixture(t)
	f.task(t, "T", map[string]task.Repo{"a": {Path: gitRepo(t), PostCreate: "echo hook output; exit 3"}})
	f.rt.ExitAtStart("a")
	if _, err := f.svc.Start(context.Background(), "T", StartOptions{}); err != nil {
		t.Fatal(err)
	}
	if failed := eventsOf[HookFailed](f.events); len(failed) != 1 || failed[0].Repo != "a" {
		t.Fatalf("hook failures %+v", failed)
	}
	if !strings.Contains(f.hooks.String(), "hook output") {
		t.Errorf("hook output %q", f.hooks.String())
	}
	if exited := eventsOf[WindowExited](f.events); len(exited) != 1 || exited[0].Window != "a" {
		t.Fatalf("exited %+v", exited)
	}

	g := newFixture(t)
	g.task(t, "U", map[string]task.Repo{"a": {Path: gitRepo(t)}})
	g.rt.Fail(runtimetest.OpAvailable, errors.New("no multiplexer"))
	if _, err := g.svc.Start(context.Background(), "U", StartOptions{}); err == nil {
		t.Fatal("start without a runtime succeeded")
	}
	if _, err := os.Stat(g.svc.Store.WorktreePath("U", "a")); err != nil {
		t.Fatalf("worktree not kept: %v", err)
	}
	if w := eventsOf[Warning](g.events); len(w) != 1 || !strings.Contains(w[0].Text, "runtime is unavailable") {
		t.Fatalf("warnings %+v", w)
	}
}

func TestStartIsCancelled(t *testing.T) {
	f := newFixture(t)
	f.task(t, "T", map[string]task.Repo{"a": {Path: gitRepo(t)}})
	f.svc.Settle = 1 << 40 // would wait for ever
	ctx, cancel := context.WithCancel(context.Background())
	f.svc.Progress = func(e Event) {
		if _, ok := e.(Launching); ok {
			cancel()
		}
	}
	if _, err := f.svc.Start(ctx, "T", StartOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled start = %v", err)
	}
}

// A refused clean leaves the session running; a successful one keeps the
// notes unless told to remove the task.
func TestClean(t *testing.T) {
	f := newFixture(t)
	f.task(t, "T", map[string]task.Repo{"a": {Path: gitRepo(t)}})
	if _, err := f.svc.Start(context.Background(), "T", StartOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Store.WriteNote("T", task.BriefFile, "the brief\n"); err != nil {
		t.Fatal(err)
	}
	dirty := filepath.Join(f.svc.Store.WorktreePath("T", "a"), "new.txt")
	if err := os.WriteFile(dirty, []byte("work"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := f.svc.Clean(context.Background(), "T", CleanOptions{})
	if err == nil || !strings.Contains(err.Error(), "work would be lost") {
		t.Fatalf("clean of dirty work = %v", err)
	}
	if running, _ := f.rt.HasSession(context.Background(), f.rt.SessionName("T")); f.stopped() || !running {
		t.Fatal("a refused clean stopped the session")
	}

	if err := os.Remove(dirty); err != nil {
		t.Fatal(err)
	}
	f.events = nil
	if err := f.svc.Clean(context.Background(), "T", CleanOptions{}); err != nil {
		t.Fatal(err)
	}
	if !f.stopped() || len(eventsOf[SessionStopped](f.events)) != 1 || len(eventsOf[WorktreeRemoved](f.events)) != 1 {
		t.Fatalf("clean: stopped %v, events %+v", f.stopped(), f.events)
	}
	if brief, _ := f.svc.Store.ReadNote("T", task.BriefFile); brief.Text != "the brief\n" || !f.svc.Store.Exists("T") {
		t.Fatal("clean lost the notes or the record")
	}
	// Again, with nothing left to remove, and this time the task goes.
	f.events = nil
	if err := f.svc.Clean(context.Background(), "T", CleanOptions{Remove: true}); err != nil {
		t.Fatal(err)
	}
	if f.svc.Store.Exists("T") || len(eventsOf[RecordRemoved](f.events)) != 1 || len(eventsOf[NotesRemoved](f.events)) != 1 {
		t.Fatalf("clean --rm: exists %v, events %+v", f.svc.Store.Exists("T"), f.events)
	}
}

// What an agent does as its session stops is judged before anything goes: a
// commit keeps its branch, and a change left behind keeps its worktree.
func TestCleanJudgesWorkDoneWhileStopping(t *testing.T) {
	f := newFixture(t)
	f.task(t, "T", map[string]task.Repo{"a": {Path: gitRepo(t)}})
	ctx := context.Background()
	if _, err := f.svc.Start(ctx, "T", StartOptions{}); err != nil {
		t.Fatal(err)
	}
	tk, _ := f.svc.Store.Load("T")
	wt := tk.Repos["a"].Worktree
	write := func() {
		if err := os.WriteFile(filepath.Join(wt, "late.txt"), []byte("late\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git := func(args ...string) {
		cmd := osexec.Command("git", args...)
		cmd.Dir = wt
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	f.rt.OnStop(write)
	err := f.svc.Clean(ctx, "T", CleanOptions{DeleteBranch: true})
	if err == nil || !strings.Contains(err.Error(), "work appeared as its agents stopped") {
		t.Fatalf("Clean = %v", err)
	}
	if _, err := os.Stat(filepath.Join(wt, "late.txt")); err != nil {
		t.Fatalf("the change left while stopping is gone: %v", err)
	}

	if err := os.Remove(filepath.Join(wt, "late.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Start(ctx, "T", StartOptions{}); err != nil {
		t.Fatal(err)
	}
	f.rt.OnStop(func() {
		write()
		git("add", "late.txt")
		git("commit", "-q", "-m", "late")
	})
	f.events = nil
	if err := f.svc.Clean(ctx, "T", CleanOptions{DeleteBranch: true}); err != nil {
		t.Fatal(err)
	}
	if ok, _ := f.svc.Git.BranchExists(ctx, tk.Repos["a"].Path, "agent/T"); !ok {
		t.Fatal("the branch with the commit made while stopping was deleted")
	}
	if len(eventsOf[BranchKept](f.events)) != 1 || len(eventsOf[BranchDeleted](f.events)) != 0 {
		t.Fatalf("events %+v", f.events)
	}
}

func TestRestartAndResume(t *testing.T) {
	f := newFixture(t)
	tk := f.task(t, "T", map[string]task.Repo{"a": {Path: gitRepo(t), Agent: []string{"repo-agent"}, Resume: []string{"repo-agent", "--continue"}}})
	if _, err := f.svc.Start(context.Background(), "T", StartOptions{}); err != nil {
		t.Fatal(err)
	}
	f.dead("T", "a")
	label, _, err := f.svc.Restart(context.Background(), "T", RestartOptions{Repo: "a", Resume: true})
	if err != nil || label != "T/a" {
		t.Fatalf("restart = %q, %v", label, err)
	}
	if got := f.restarted()[0].Command; !slices.Equal(got, []string{"repo-agent", "--continue"}) {
		t.Fatalf("resumed with %v", got)
	}
	// The root window's agent has no resume command.
	_, _, err = f.svc.Restart(context.Background(), "T", RestartOptions{Root: true, Resume: true})
	if err == nil || !strings.Contains(err.Error(), "tspace task update "+tk.Name+" --resume") {
		t.Fatalf("restart --resume without one = %v", err)
	}
	if running, err := f.svc.Stop(context.Background(), "T"); err != nil || !running {
		t.Fatalf("stop = %v, %v", running, err)
	}
	if running, err := f.svc.Stop(context.Background(), "T"); err != nil || running {
		t.Fatalf("stop again = %v, %v", running, err)
	}
}

// A busy task fails at once without waiting, naming who has it.
func TestNoWait(t *testing.T) {
	f := newFixture(t)
	f.task(t, "T", map[string]task.Repo{"a": {Path: gitRepo(t)}})
	l, err := f.svc.Store.Lock(context.Background(), "T", "tspace clean T", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Unlock()
	f.svc.NoWait = true
	if _, err := f.svc.Stop(context.Background(), "T"); err == nil || !strings.Contains(err.Error(), "tspace clean T") {
		t.Fatalf("stop of a busy task = %v", err)
	}
}

func TestPostCreateFailureIsReported(t *testing.T) {
	f := newFixture(t)
	r := task.Repo{Path: gitRepo(t), Worktree: t.TempDir(), PostCreate: "exit 7"}
	err := f.svc.RunPostCreate(context.Background(), task.New("T"), "backend", r)
	var exit *osexec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 7 {
		t.Fatalf("RunPostCreate = %v, want exit 7", err)
	}
}

// A repository named like the root window is no way to the root window.
func TestRestartNeedsRootSaidOutright(t *testing.T) {
	f := newFixture(t)
	f.task(t, "T", map[string]task.Repo{"a": {Path: gitRepo(t)}})
	if _, err := f.svc.Start(context.Background(), "T", StartOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.svc.Restart(context.Background(), "T", RestartOptions{Repo: task.RootWindowName}); err == nil {
		t.Fatal("restart of a repository named _root restarted something")
	}
	if len(f.restarted()) != 0 {
		t.Fatalf("restarted %+v", f.restarted())
	}
}

func TestPostCreateClearsRepositoryEnvironment(t *testing.T) {
	f := newFixture(t)
	repo, other := gitRepo(t), gitRepo(t)
	t.Setenv("GIT_DIR", filepath.Join(other, ".git"))
	t.Setenv("GIT_WORK_TREE", other)
	var out bytes.Buffer
	f.svc.Stdout = &out
	r := task.Repo{Path: repo, Worktree: repo, PostCreate: "git rev-parse --show-toplevel"}
	if err := f.svc.RunPostCreate(context.Background(), task.New("T"), "backend", r); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != repo {
		t.Fatalf("hook used %q, want %q", out.String(), repo)
	}
}
