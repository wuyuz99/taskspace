package workspace

import (
	"context"
	"testing"

	"github.com/wuyuz99/taskspace/internal/task"
)

// The result names the window, so a caller need not read it from the output.
func TestOpenWindowNamesWhatItOpened(t *testing.T) {
	f := newFixture(t)
	f.task(t, "T", map[string]task.Repo{"a": {Path: gitRepo(t)}})
	ctx := context.Background()
	if _, err := f.svc.Start(ctx, "T", StartOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"_a-make", "_a-make-2"} {
		res, err := f.svc.OpenWindow(ctx, "T", OpenWindowOptions{Repo: "a", Command: []string{"make"}, Detach: true})
		if err != nil {
			t.Fatal(err)
		}
		if res.Window != want {
			t.Fatalf("window %q, want %q", res.Window, want)
		}
	}
	if err := f.svc.RenameWindow(ctx, "T", "_a-make", "_build"); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.SaveOpenWindow(ctx, "T", "_build"); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.ForgetWindow(ctx, "T", "_build"); err != nil {
		t.Fatal(err)
	}
}

// Whoever calls it, opening a window waits for whatever is changing the task.
func TestOpenWindowTakesTheLock(t *testing.T) {
	f := newFixture(t)
	f.task(t, "T", map[string]task.Repo{"a": {Path: gitRepo(t)}})
	ctx := context.Background()
	if _, err := f.svc.Start(ctx, "T", StartOptions{}); err != nil {
		t.Fatal(err)
	}
	f.svc.NoWait = true
	l, err := f.svc.Store.Lock(ctx, "T", "someone else", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Unlock()
	if _, err := f.svc.OpenWindow(ctx, "T", OpenWindowOptions{Repo: "a", Detach: true}); err == nil {
		t.Fatal("opened a window in a task someone else holds")
	}
}
