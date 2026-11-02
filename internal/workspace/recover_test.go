package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	osexec "os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wuyuz99/taskspace/internal/runtime"
	"github.com/wuyuz99/taskspace/internal/runtime/runtimetest"
	"github.com/wuyuz99/taskspace/internal/task"
)

// crash ends every session the way a computer restart does.
func (f *fixture) crash() {
	f.rt.Reboot()
	f.rt.ForgetCalls()
}

func names(ws []runtime.Window) []string {
	var out []string
	for _, w := range ws {
		out = append(out, w.Name)
	}
	slices.Sort(out)
	return out
}

// recover starts the interrupted tasks and wants them all started.
func (f *fixture) recover(t *testing.T) []string {
	t.Helper()
	started, failed, err := f.svc.StartInterrupted(context.Background(), StartOptions{})
	if err != nil || len(failed) > 0 {
		t.Fatalf("StartInterrupted: started %v, failed %v, %v", started, failed, err)
	}
	return started
}

// A task with no repositories runs only its root window, and comes back
// with it.
func TestInterruptedRootOnlyTaskComesBack(t *testing.T) {
	f := newFixture(t)
	f.task(t, "T", nil)
	ctx := context.Background()
	if _, err := f.svc.Start(ctx, "T", StartOptions{Root: true}); err != nil {
		t.Fatal(err)
	}
	f.crash()
	if started := f.recover(t); !slices.Equal(started, []string{"T"}) {
		t.Fatalf("started %v", started)
	}
	if got := names(f.started()); !slices.Equal(got, []string{task.RootWindowName}) {
		t.Fatalf("launched %v", got)
	}
	if !f.svc.Store.Interrupted("T") {
		t.Fatal("not running once started again")
	}
}

// What comes back is what ran: not the repositories left stopped, nor their
// worktrees, nor windows closed since.
func TestInterruptedBringsBackWhatRan(t *testing.T) {
	f := newFixture(t)
	resumeTask(t, f)
	ctx := context.Background()
	if _, err := f.svc.Start(ctx, "T", StartOptions{Repos: []string{"a"}}); err != nil {
		t.Fatal(err)
	}
	f.crash()
	f.recover(t)
	if got := names(f.started()); !slices.Equal(got, []string{"a"}) {
		t.Fatalf("launched %v", got)
	}
	tk, err := f.svc.Store.Load("T")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(tk.Repos["b"].Worktree); !os.IsNotExist(err) {
		t.Fatalf("b's worktree made: %v", err)
	}

	// The root window, started on its own beside a, comes back with it,
	// which takes two starts.
	if _, err := f.svc.Start(ctx, "T", StartOptions{Root: true}); err != nil {
		t.Fatal(err)
	}
	f.crash()
	f.recover(t)
	if got := names(f.started()); !slices.Equal(got, []string{task.RootWindowName, "a"}) {
		t.Fatalf("launched %v", got)
	}

	// The whole task, less a window closed on purpose.
	if _, err := f.svc.Start(ctx, "T", StartOptions{}); err != nil {
		t.Fatal(err)
	}
	f.crash()
	if _, err := f.svc.Start(ctx, "T", StartOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.StopWindow(ctx, "T", "b"); err != nil {
		t.Fatal(err)
	}
	f.crash()
	f.recover(t)
	if got := names(f.started()); !slices.Equal(got, []string{task.RootWindowName, "a"}) {
		t.Fatalf("launched %v", got)
	}
}

// Every interrupted task is launched before any is waited on, so starting
// them takes one Settle, not one each.
func TestInterruptedSettleTogether(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	tasks := []string{"A", "B", "C"}
	for _, name := range tasks {
		// Window names are the fake's only key, so each task has its own.
		f.task(t, name, map[string]task.Repo{strings.ToLower(name): {Path: gitRepo(t)}})
		if _, err := f.svc.Start(ctx, name, StartOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	f.crash()
	f.svc.Settle = time.Second
	begin := time.Now()
	if started := f.recover(t); !slices.Equal(started, tasks) {
		t.Fatalf("started %v", started)
	}
	if took := time.Since(begin); took >= 2*time.Second {
		t.Fatalf("took %v, as if each task settled in turn", took)
	}
}

// A session clean cannot check may be running: its worktrees stay, and the
// task is not taken as stopped on purpose.
func TestCleanRefusesASessionItCannotCheck(t *testing.T) {
	f := newFixture(t)
	resumeTask(t, f)
	ctx := context.Background()
	if _, err := f.svc.Start(ctx, "T", StartOptions{}); err != nil {
		t.Fatal(err)
	}
	f.rt.Fail(runtimetest.OpHasSession, errors.New("server busy"))
	err := f.svc.Clean(ctx, "T", CleanOptions{})
	if err == nil || !strings.Contains(err.Error(), "server busy") {
		t.Fatalf("Clean = %v", err)
	}
	tk, _ := f.svc.Store.Load("T")
	if _, err := os.Stat(tk.Repos["a"].Worktree); err != nil {
		t.Fatalf("worktree removed: %v", err)
	}
	if !f.svc.Store.Interrupted("T") {
		t.Fatal("recorded as stopped")
	}

	// A multiplexer that says it is unusable may still run the session.
	f.rt.Fail(runtimetest.OpHasSession, errors.New("state: exit status 23"))
	f.rt.Fail(runtimetest.OpAvailable, errors.New("check: exit status 23"))
	if err := f.svc.Clean(ctx, "T", CleanOptions{}); err == nil {
		t.Fatal("Clean with an unusable runtime removed the worktrees")
	}
	if _, err := os.Stat(tk.Repos["a"].Worktree); err != nil {
		t.Fatalf("worktree removed: %v", err)
	}

	// One that is not installed runs nothing.
	notInstalled := fmt.Errorf("exec tmux: %w", osexec.ErrNotFound)
	f.rt.Fail(runtimetest.OpHasSession, notInstalled)
	f.rt.Fail(runtimetest.OpAvailable, notInstalled)
	if err := f.svc.Clean(ctx, "T", CleanOptions{}); err != nil {
		t.Fatalf("Clean without a multiplexer = %v", err)
	}
	if _, err := os.Stat(tk.Repos["a"].Worktree); !os.IsNotExist(err) {
		t.Fatalf("worktree kept: %v", err)
	}
	if f.stopped() {
		t.Fatal("stopped a session in a multiplexer that is not installed")
	}
}

// Closing the last window an interruption would bring back leaves nothing
// to bring back, while extra windows keep the session up; restart brings a
// closed one back into what ran.
func TestClosedWindowsAreNotInterrupted(t *testing.T) {
	f := newFixture(t)
	resumeTask(t, f)
	ctx := context.Background()
	if _, err := f.svc.Start(ctx, "T", StartOptions{Repos: []string{"a"}}); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.StopWindow(ctx, "T", "a"); err != nil {
		t.Fatal(err)
	}
	if f.svc.Store.Interrupted("T") {
		t.Fatal("interrupted with nothing to bring back")
	}

	if _, err := f.svc.Start(ctx, "T", StartOptions{Repos: []string{"a", "b"}}); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.StopWindow(ctx, "T", "b"); err != nil {
		t.Fatal(err)
	}
	if err := f.rt.OpenWindow(ctx, f.rt.SessionName("T"), runtime.Window{Name: "b", Command: []string{"false"}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.svc.Restart(ctx, "T", RestartOptions{Window: "b", Fresh: true}); err != nil {
		t.Fatal(err)
	}
	if r, _ := f.svc.Store.Running("T"); !slices.Equal(r.Repos, []string{"a", "b"}) {
		t.Fatalf("running %+v", r)
	}
}

// Everything comes back in one launch, since a multiplexer may be unable to
// add to a session later. A window that cannot be made fails that launch, and
// the task stays interrupted to try again.
func TestInterruptedFailedLaunchStaysInterrupted(t *testing.T) {
	f := newFixture(t)
	resumeTask(t, f)
	ctx := context.Background()
	if _, err := f.svc.Start(ctx, "T", StartOptions{}); err != nil {
		t.Fatal(err)
	}
	f.crash()
	if err := f.svc.Store.MarkRunning("T", task.Running{Root: true, Repos: []string{"a"}}); err != nil {
		t.Fatal(err)
	}
	tk, _ := f.svc.Store.Load("T")
	a := tk.Repos["a"]
	if err := os.RemoveAll(a.Worktree); err != nil {
		t.Fatal(err)
	}
	a.Path = t.TempDir() // not a repository: a's worktree cannot be made
	tk.SetRepo("a", a)
	if err := f.svc.Store.Save(tk); err != nil {
		t.Fatal(err)
	}
	_, failed, err := f.svc.StartInterrupted(ctx, StartOptions{})
	if err != nil || !slices.Equal(failed, []string{"T"}) {
		t.Fatalf("failed %v, %v", failed, err)
	}
	if got := f.started(); len(got) != 0 {
		t.Fatalf("launched %v", got)
	}
	if !f.svc.Store.Interrupted("T") {
		t.Fatal("no longer interrupted")
	}
}

// A window that had exited was not running, so it does not come back.
func TestInterruptedLeavesOutDeadWindows(t *testing.T) {
	f := newFixture(t)
	resumeTask(t, f)
	ctx := context.Background()
	if _, err := f.svc.Start(ctx, "T", StartOptions{}); err != nil {
		t.Fatal(err)
	}
	f.dead("T", "a")
	session := f.rt.SessionName("T")
	st, err := f.rt.State(ctx, session)
	if err != nil {
		t.Fatal(err)
	}
	tk, _ := f.svc.Store.Load("T")
	f.svc.Observe(ctx, tk, st)
	f.crash()
	f.recover(t)
	if got := names(f.started()); !slices.Equal(got, []string{task.RootWindowName, "b"}) {
		t.Fatalf("launched %v", got)
	}
}

// With every window dead nothing was running, and nothing is offered.
func TestInterruptedNeedsSomethingAlive(t *testing.T) {
	f := newFixture(t)
	f.task(t, "T", map[string]task.Repo{"a": {Path: gitRepo(t)}})
	f.rt.ExitAtStart(task.RootWindowName, "a")
	if _, err := f.svc.Start(context.Background(), "T", StartOptions{}); err != nil {
		t.Fatal(err)
	}
	if f.svc.Store.Interrupted("T") {
		t.Fatal("a session of dead windows counts as running")
	}
}

// Saved windows come back as they ran: those open, at the root too, and not
// one closed on purpose.
func TestInterruptedBringsBackSavedWindows(t *testing.T) {
	f := newFixture(t)
	resumeTask(t, f)
	ctx := context.Background()
	tk, _ := f.svc.Store.Load("T")
	tk.Windows = []task.Window{{Name: "logs"}, {Name: "git", Repo: "a"}, {Name: "notes", Repo: "b"}}
	if err := f.svc.Store.Save(tk); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Start(ctx, "T", StartOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.StopWindow(ctx, "T", "_notes"); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.StopWindow(ctx, "T", "b"); err != nil {
		t.Fatal(err)
	}
	f.crash()
	f.recover(t)
	if got := names(f.started()); !slices.Equal(got, []string{"_git", "_logs", task.RootWindowName, "a"}) {
		t.Fatalf("launched %v", got)
	}
	if calls := len(f.rt.Calls(runtimetest.OpOpenWindow)); calls != 0 {
		t.Fatalf("added %d windows after the session started", calls)
	}
}

// A record from before snapshots brings back the whole task.
func TestInterruptedOlderRecord(t *testing.T) {
	f := newFixture(t)
	resumeTask(t, f)
	if err := f.svc.Store.MarkRunning("T", task.Running{Whole: true}); err != nil {
		t.Fatal(err)
	}
	f.recover(t)
	if got := names(f.started()); !slices.Equal(got, []string{task.RootWindowName, "a", "b"}) {
		t.Fatalf("launched %v", got)
	}
}

// A state read before a stop, written after it, must not make the stop look
// like an interruption.
func TestObserveAfterStopWritesNothing(t *testing.T) {
	f := newFixture(t)
	resumeTask(t, f)
	ctx := context.Background()
	if _, err := f.svc.Start(ctx, "T", StartOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Store.ClearRunning("T"); err != nil {
		t.Fatal(err)
	}
	st, err := f.rt.State(ctx, f.rt.SessionName("T"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Stop(ctx, "T"); err != nil {
		t.Fatal(err)
	}
	tk, _ := f.svc.Store.Load("T")
	f.svc.Observe(ctx, tk, st)
	if f.svc.Store.Interrupted("T") {
		t.Fatal("a stopped task counts as interrupted")
	}
}

// Observing the same thing again writes nothing, so dash can do it every
// refresh, even with the task locked by someone else.
func TestObserveWaitsForNothingWhenUnchanged(t *testing.T) {
	f := newFixture(t)
	resumeTask(t, f)
	ctx := context.Background()
	if _, err := f.svc.Start(ctx, "T", StartOptions{}); err != nil {
		t.Fatal(err)
	}
	st, err := f.rt.State(ctx, f.rt.SessionName("T"))
	if err != nil {
		t.Fatal(err)
	}
	tk, _ := f.svc.Store.Load("T")
	if err := os.Remove(f.svc.Store.LockPath("T")); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	f.svc.Observe(ctx, tk, st)
	if _, err := os.Stat(f.svc.Store.LockPath("T")); !os.IsNotExist(err) {
		t.Fatalf("took the lock for no change: %v", err)
	}
}
