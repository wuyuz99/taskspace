package workspace

import (
	"context"
	"errors"
	"testing"

	"github.com/wuyuz99/taskspace/internal/runtime/runtimetest"
)

// A session is labelled with its task's name and title when it starts, and
// again when the title changes while it runs.
func TestSessionLabelFollowsTitle(t *testing.T) {
	f := newFixture(t)
	tk := f.task(t, "PAY-142", nil)
	tk.Title = "Totals endpoint"
	if err := f.svc.Store.Save(tk); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := f.svc.Start(ctx, "PAY-142", StartOptions{Root: true}); err != nil {
		t.Fatal(err)
	}
	session := f.rt.SessionName("PAY-142")
	if got := f.rt.Label(session); got != "PAY-142 - Totals endpoint" {
		t.Fatalf("label after start = %q", got)
	}
	if _, err := f.svc.UpdateTask(ctx, "PAY-142", UpdateTaskOptions{SetTitle: true, Title: "Round per line"}); err != nil {
		t.Fatal(err)
	}
	if got := f.rt.Label(session); got != "PAY-142 - Round per line" {
		t.Fatalf("label after a new title = %q", got)
	}
	if _, err := f.svc.UpdateTask(ctx, "PAY-142", UpdateTaskOptions{SetTitle: true}); err != nil {
		t.Fatal(err)
	}
	if got := f.rt.Label(session); got != "PAY-142" {
		t.Fatalf("label after the title is cleared = %q", got)
	}
}

// A multiplexer that cannot show a label still starts the task.
func TestStartIgnoresALabelFailure(t *testing.T) {
	f := newFixture(t)
	f.task(t, "T", nil)
	f.rt.Fail(runtimetest.OpLabel, errors.New("no label here"))
	if _, err := f.svc.Start(context.Background(), "T", StartOptions{Root: true}); err != nil {
		t.Fatalf("Start = %v, want the label failure ignored", err)
	}
	if ok, _ := f.rt.HasSession(context.Background(), f.rt.SessionName("T")); !ok {
		t.Fatal("no session")
	}
}
