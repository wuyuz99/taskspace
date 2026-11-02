package status

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	tsexec "github.com/wuyuz99/taskspace/internal/exec"
	tsgit "github.com/wuyuz99/taskspace/internal/git"
	"github.com/wuyuz99/taskspace/internal/runtime"
	"github.com/wuyuz99/taskspace/internal/runtime/runtimetest"
	"github.com/wuyuz99/taskspace/internal/task"
)

// perTask counts the per-task queries a batched refresh should not need.
type perTask struct {
	*runtimetest.Fake
	queries atomic.Int32
}

func (p *perTask) State(ctx context.Context, session string) (runtime.SessionState, error) {
	p.queries.Add(1)
	return p.Fake.State(ctx, session)
}

func tasks(t *testing.T, names ...string) []*task.Task {
	t.Helper()
	var out []*task.Task
	for _, n := range names {
		tk := task.New(n)
		tk.SetRepo("api", task.Repo{Worktree: filepath.Join(t.TempDir(), "missing")})
		out = append(out, tk)
	}
	return out
}

func TestAllAsksTheMultiplexerOnce(t *testing.T) {
	rt := &perTask{Fake: runtimetest.New()}
	rt.Seed(rt.SessionName("a"), map[string]runtime.PaneState{"api": {}})
	got := New(tsgit.New(tsexec.Real()), rt).All(context.Background(), tasks(t, "a", "b", "c"))
	if n := rt.queries.Load(); n != 0 {
		t.Fatalf("%d queries beside the batched one", n)
	}
	if got[0].Repos[0].Runtime != RuntimeRunning || got[1].Repos[0].Runtime != RuntimeNoSession {
		t.Fatalf("got %+v", got)
	}
}

// A batched query that fails says nothing about each session, so they are
// asked one by one, and a failure there is reported as it always was.
func TestAllFallsBackToEachTask(t *testing.T) {
	rt := &perTask{Fake: runtimetest.New()}
	rt.Fail(runtimetest.OpState, context.DeadlineExceeded)
	got := New(tsgit.New(tsexec.Real()), rt).All(context.Background(), tasks(t, "a", "b"))
	if n := rt.queries.Load(); n != 2 {
		t.Fatalf("%d queries, want one per task", n)
	}
	if got[0].RuntimeErr == nil || got[0].Repos[0].Runtime != RuntimeUnknown {
		t.Fatalf("got %+v", got[0])
	}
}

// busiest records how many git commands ran at once.
type busiest struct {
	tsexec.Runner
	mu         sync.Mutex
	now, worst int
}

func (b *busiest) Run(ctx context.Context, dir string, argv ...string) (tsexec.Result, error) {
	b.mu.Lock()
	b.now++
	b.worst = max(b.worst, b.now)
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		b.now--
		b.mu.Unlock()
	}()
	return b.Runner.Run(ctx, dir, argv...)
}

func TestGitIsBounded(t *testing.T) {
	repo := initRepo(t)
	tk := task.New("t")
	for _, n := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m", "n", "o", "p"} {
		tk.SetRepo(n, task.Repo{Worktree: repo, Branch: "main"})
	}
	run := &busiest{Runner: tsexec.Real()}
	got := New(tsgit.New(run), noSession()).Task(context.Background(), tk)
	for _, r := range got.Repos {
		if r.Git != GitClean {
			t.Fatalf("%s: %s, %v", r.Repo, r.Git, r.Err)
		}
	}
	if run.worst == 0 || run.worst > gitLimit {
		t.Fatalf("%d git commands at once, limit %d", run.worst, gitLimit)
	}
}
