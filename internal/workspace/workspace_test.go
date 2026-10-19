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
	"github.com/wuyuz99/taskspace/internal/task"
)

// fakeRuntime is a session that exists once started, with the windows it
// was given, each alive unless named in dead.
type fakeRuntime struct {
	unavailable error
	panes       map[string]runtime.PaneState
	exists      bool
	dead        map[string]bool
	started     []runtime.Window
	restarted   []runtime.Window
	stopped     bool
}

func (f *fakeRuntime) Available(context.Context) error { return f.unavailable }
func (f *fakeRuntime) SessionName(t string) string     { return "tspace-" + t }
func (f *fakeRuntime) WindowName(r string) string      { return r }
func (f *fakeRuntime) HasSession(context.Context, string) (bool, error) {
	return f.exists, nil
}
func (f *fakeRuntime) Start(_ context.Context, _ string, ws []runtime.Window) error {
	if f.panes == nil {
		f.panes = map[string]runtime.PaneState{}
	}
	f.exists = true
	for _, w := range ws {
		if _, ok := f.panes[w.Name]; !ok {
			f.started = append(f.started, w)
			f.panes[w.Name] = runtime.PaneState{Window: w.Name, Dead: f.dead[w.Name]}
		}
	}
	return nil
}
func (f *fakeRuntime) OpenWindow(context.Context, string, runtime.Window) error { return nil }
func (f *fakeRuntime) Restart(_ context.Context, _ string, w runtime.Window) error {
	f.restarted = append(f.restarted, w)
	return nil
}
func (f *fakeRuntime) Stop(context.Context, string) error {
	f.stopped, f.exists, f.panes = true, false, nil
	return nil
}
func (f *fakeRuntime) Attach(context.Context, string) error { return nil }
func (f *fakeRuntime) Focus(context.Context, string, string) (bool, error) {
	return false, nil
}
func (f *fakeRuntime) Select(context.Context, string, string) error { return nil }
func (f *fakeRuntime) State(context.Context, string) (runtime.SessionState, error) {
	return runtime.SessionState{Exists: f.exists, Panes: f.panes}, nil
}

type fixture struct {
	svc    *Service
	rt     *fakeRuntime
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
	f := &fixture{rt: &fakeRuntime{}}
	f.svc = &Service{
		Store: task.NewStore(root), Git: tsgit.New(tsexec.Real()), Runtime: f.rt,
		Stdout: &f.hooks, Stderr: &f.hooks,
		Progress: func(e Event) { f.events = append(f.events, e) },
	}
	return f
}

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

func TestStartAndRepeat(t *testing.T) {
	f := newFixture(t)
	f.task(t, "T", map[string]task.Repo{"a": {Path: gitRepo(t)}, "b": {Path: gitRepo(t)}})
	res, err := f.svc.Start(context.Background(), "T", StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Session != "tspace-T" || len(eventsOf[WorktreeReady](f.events)) != 2 {
		t.Fatalf("result %+v, events %+v", res, f.events)
	}
	names := []string{}
	for _, w := range f.rt.started {
		names = append(names, w.Name)
	}
	if !slices.Equal(names, []string{task.RootWindowName, "a", "b"}) {
		t.Fatalf("started %v", names)
	}
	// The root agent's placeholders are filled in.
	if root := f.rt.started[0]; !slices.Contains(root.Command, f.svc.Store.ContextDir("T")) {
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
	if len(f.rt.started) != 3 {
		t.Fatalf("windows started again: %d", len(f.rt.started))
	}
}

// A failing hook is a warning; a window that dies at once is reported; an
// unavailable runtime fails after the worktrees, which stay.
func TestStartPartialFailures(t *testing.T) {
	f := newFixture(t)
	f.task(t, "T", map[string]task.Repo{"a": {Path: gitRepo(t), PostCreate: "echo hook output; exit 3"}})
	f.rt.dead = map[string]bool{"a": true}
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
	g.rt.unavailable = errors.New("no multiplexer")
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
	if f.rt.stopped || !f.rt.exists {
		t.Fatal("a refused clean stopped the session")
	}

	if err := os.Remove(dirty); err != nil {
		t.Fatal(err)
	}
	f.events = nil
	if err := f.svc.Clean(context.Background(), "T", CleanOptions{}); err != nil {
		t.Fatal(err)
	}
	if !f.rt.stopped || len(eventsOf[SessionStopped](f.events)) != 1 || len(eventsOf[WorktreeRemoved](f.events)) != 1 {
		t.Fatalf("clean: stopped %v, events %+v", f.rt.stopped, f.events)
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

func TestRestartAndResume(t *testing.T) {
	f := newFixture(t)
	tk := f.task(t, "T", map[string]task.Repo{"a": {Path: gitRepo(t), Agent: []string{"repo-agent"}, Resume: []string{"repo-agent", "--continue"}}})
	if _, err := f.svc.Start(context.Background(), "T", StartOptions{}); err != nil {
		t.Fatal(err)
	}
	label, err := f.svc.Restart(context.Background(), "T", RestartOptions{Repo: "a", Resume: true})
	if err != nil || label != "T/a" {
		t.Fatalf("restart = %q, %v", label, err)
	}
	if got := f.rt.restarted[0].Command; !slices.Equal(got, []string{"repo-agent", "--continue"}) {
		t.Fatalf("resumed with %v", got)
	}
	// The root window's agent has no resume command.
	_, err = f.svc.Restart(context.Background(), "T", RestartOptions{Root: true, Resume: true})
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
	if _, err := f.svc.Restart(context.Background(), "T", RestartOptions{Repo: task.RootWindowName}); err == nil {
		t.Fatal("restart of a repository named _root restarted something")
	}
	if len(f.rt.restarted) != 0 {
		t.Fatalf("restarted %+v", f.rt.restarted)
	}
}
