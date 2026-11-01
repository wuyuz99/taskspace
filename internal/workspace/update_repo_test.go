package workspace

import (
	"context"
	"slices"
	"testing"

	"github.com/wuyuz99/taskspace/internal/task"
)

// Whoever calls it, the operation is safe: it takes the task's lock itself.
func TestUpdateRepoTakesTheLock(t *testing.T) {
	f := newFixture(t)
	f.task(t, "T", map[string]task.Repo{"a": {Path: gitRepo(t)}})
	f.svc.NoWait = true
	ctx := context.Background()
	l, err := f.svc.Store.Lock(ctx, "T", "someone else", nil)
	if err != nil {
		t.Fatal(err)
	}
	opts := UpdateRepoOptions{SetAgent: true, Agent: []string{"codex"}}
	if _, err := f.svc.UpdateRepo(ctx, "T", "a", opts); err == nil {
		t.Fatal("updated a task someone else holds")
	}
	l.Unlock()
	res, err := f.svc.UpdateRepo(ctx, "T", "a", opts)
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Task.RepoAgent("a"); !slices.Equal(got, []string{"codex"}) {
		t.Fatalf("agent %v", got)
	}
	saved, err := f.svc.Store.Load("T")
	if err != nil {
		t.Fatal(err)
	}
	if got := saved.RepoAgent("a"); !slices.Equal(got, []string{"codex"}) {
		t.Fatalf("saved agent %v", got)
	}
}
