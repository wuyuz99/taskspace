package workspace

import (
	"context"
	"testing"
)

// A later repository that fails leaves no task, and nothing saying the first
// one was added to it.
func TestCreateTaskPublishesAllOrNothing(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	repo := gitRepo(t)
	_, err := f.svc.CreateTask(ctx, CreateTaskOptions{Name: "T", Repos: []string{"a=" + repo, "b=" + t.TempDir()}})
	if err == nil {
		t.Fatal("created a task with a repository that is not one")
	}
	if f.svc.Store.Exists("T") {
		t.Fatal("record left behind")
	}
	if got := eventsOf[RepoAdded](f.events); len(got) != 0 {
		t.Fatalf("reported %+v", got)
	}

	res, err := f.svc.CreateTask(ctx, CreateTaskOptions{Name: "T", Repos: []string{"a=" + repo}, Start: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Started == nil || !f.svc.Store.Exists("T") {
		t.Fatalf("result %+v", res)
	}
	if got := eventsOf[RepoAdded](f.events); len(got) != 1 {
		t.Fatalf("reported %+v", got)
	}
	if got := eventsOf[TaskCreated](f.events); len(got) != 1 || !got[0].Starting {
		t.Fatalf("reported %+v", got)
	}
	if _, err := f.svc.CreateTask(ctx, CreateTaskOptions{Name: "T"}); err == nil {
		t.Fatal("created over an existing task")
	}
}
