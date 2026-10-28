package workspace

import (
	"context"
	"slices"
	"testing"

	"github.com/wuyuz99/taskspace/internal/runtime"
	"github.com/wuyuz99/taskspace/internal/task"
)

// resumeTask has a root agent and a repository agent, each with a resume
// command, and two repositories, so it has a root window.
func resumeTask(t *testing.T, f *fixture) {
	t.Helper()
	tk := f.task(t, "T", map[string]task.Repo{
		"a": {Path: gitRepo(t), Agent: []string{"a-agent"}, Resume: []string{"a-agent", "--continue"}},
		"b": {Path: gitRepo(t)},
	})
	tk.Agent, tk.Resume = []string{"root-agent"}, []string{"root-agent", "--continue"}
	if err := f.svc.Store.Save(tk); err != nil {
		t.Fatal(err)
	}
}

func commands(ws []runtime.Window) map[string][]string {
	out := map[string][]string{}
	for _, w := range ws {
		out[w.Name] = w.Command
	}
	return out
}

func TestStartResumesWhatWasThere(t *testing.T) {
	f := newFixture(t)
	resumeTask(t, f)
	ctx := context.Background()
	start := func(opts StartOptions) map[string][]string {
		t.Helper()
		f.rt.started, f.events = nil, nil
		if _, err := f.svc.Start(ctx, "T", opts); err != nil {
			t.Fatal(err)
		}
		return commands(f.rt.started)
	}

	// Worktrees and the task directory made now hold no conversation.
	got := start(StartOptions{})
	if !slices.Equal(got["a"], []string{"a-agent"}) || !slices.Equal(got[task.RootWindowName], []string{"root-agent"}) {
		t.Fatalf("first start: %v", got)
	}
	if len(eventsOf[Resumed](f.events)) != 0 {
		t.Fatal("resumed on the first start")
	}
	// A window still running is not launched again, so not resumed either.
	start(StartOptions{})
	if len(f.rt.started) != 0 || len(eventsOf[Resumed](f.events)) != 0 {
		t.Fatalf("started %v", f.rt.started)
	}

	if _, err := f.svc.Stop(ctx, "T"); err != nil {
		t.Fatal(err)
	}
	got = start(StartOptions{})
	if !slices.Equal(got["a"], []string{"a-agent", "--continue"}) || !slices.Equal(got[task.RootWindowName], []string{"root-agent", "--continue"}) {
		t.Fatalf("second start: %v", got)
	}
	if len(got["b"]) != 0 {
		t.Fatalf("b has no resume command, yet ran %v", got["b"])
	}
	if r := eventsOf[Resumed](f.events); len(r) != 2 {
		t.Fatalf("resumed events %+v", r)
	}

	if _, err := f.svc.Stop(ctx, "T"); err != nil {
		t.Fatal(err)
	}
	if got = start(StartOptions{Fresh: true}); !slices.Equal(got["a"], []string{"a-agent"}) {
		t.Fatalf("--fresh: %v", got)
	}
}

// A resume command that exits at once is followed by the window's own.
func TestStartFallsBackFromAFailedResume(t *testing.T) {
	f := newFixture(t)
	resumeTask(t, f)
	ctx := context.Background()
	if _, err := f.svc.Start(ctx, "T", StartOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Stop(ctx, "T"); err != nil {
		t.Fatal(err)
	}
	f.rt.dead, f.events = map[string]bool{"a": true}, nil
	if _, err := f.svc.Start(ctx, "T", StartOptions{}); err != nil {
		t.Fatal(err)
	}
	if len(f.rt.restarted) != 1 || !slices.Equal(f.rt.restarted[0].Command, []string{"a-agent"}) {
		t.Fatalf("restarted %+v", f.rt.restarted)
	}
	if r := eventsOf[ResumeFailed](f.events); len(r) != 1 || r[0].Window != "a" {
		t.Fatalf("events %+v", f.events)
	}
	if len(eventsOf[WindowExited](f.events)) != 0 {
		t.Fatal("reported as exited although it runs fresh")
	}
}

func TestRestartResumesUnlessFresh(t *testing.T) {
	f := newFixture(t)
	resumeTask(t, f)
	ctx := context.Background()
	if _, err := f.svc.Start(ctx, "T", StartOptions{}); err != nil {
		t.Fatal(err)
	}
	f.rt.panes["a"] = runtime.PaneState{Window: "a", Dead: true}
	_, resumed, err := f.svc.Restart(ctx, "T", RestartOptions{Repo: "a"})
	if err != nil || !resumed || !slices.Equal(f.rt.restarted[0].Command, []string{"a-agent", "--continue"}) {
		t.Fatalf("restart: resumed=%v err=%v %+v", resumed, err, f.rt.restarted)
	}
	_, resumed, err = f.svc.Restart(ctx, "T", RestartOptions{Repo: "a", Fresh: true})
	if err != nil || resumed || !slices.Equal(f.rt.restarted[1].Command, []string{"a-agent"}) {
		t.Fatalf("--fresh: resumed=%v err=%v %+v", resumed, err, f.rt.restarted)
	}
	// Without a resume command, b is relaunched as it is.
	if _, resumed, err = f.svc.Restart(ctx, "T", RestartOptions{Repo: "b"}); err != nil || resumed {
		t.Fatalf("b: resumed=%v err=%v", resumed, err)
	}
}

// Only an agent that has run in a directory is resumed there: not after
// --no-runtime made the worktree, nor a root window launched for the first
// time, and not in a worktree made again.
func TestResumeNeedsAnAgentThatRan(t *testing.T) {
	f := newFixture(t)
	resumeTask(t, f)
	ctx := context.Background()
	if _, err := f.svc.Start(ctx, "T", StartOptions{NoRuntime: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Start(ctx, "T", StartOptions{Repos: []string{"a"}}); err != nil {
		t.Fatal(err)
	}
	if len(eventsOf[Resumed](f.events)) != 0 {
		t.Fatalf("resumed after --no-runtime: %+v", f.events)
	}
	if _, err := f.svc.Stop(ctx, "T"); err != nil {
		t.Fatal(err)
	}
	f.events, f.rt.started = nil, nil
	if _, err := f.svc.Start(ctx, "T", StartOptions{}); err != nil {
		t.Fatal(err)
	}
	got := commands(f.rt.started)
	if !slices.Equal(got["a"], []string{"a-agent", "--continue"}) || !slices.Equal(got[task.RootWindowName], []string{"root-agent"}) {
		t.Fatalf("a ran before and the root window did not: %v", got)
	}

	// A worktree made again holds nothing to resume.
	if _, err := f.svc.Stop(ctx, "T"); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Clean(ctx, "T", CleanOptions{Repos: []string{"a"}}); err != nil {
		t.Fatal(err)
	}
	f.rt.started = nil
	if _, err := f.svc.Start(ctx, "T", StartOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := commands(f.rt.started); !slices.Equal(got["a"], []string{"a-agent"}) {
		t.Fatalf("recreated worktree resumed: %v", got)
	}
}
