package runtimetest

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/wuyuz99/taskspace/internal/runtime"
)

func TestFakeConformance(t *testing.T) {
	Conformance(t, func(*testing.T) Target {
		return Target{Runtime: New(), Session: func(*testing.T) string { return "tspace@t" }}
	})
}

// What a test could otherwise see changing under it, as #84's fake did.
func TestStateIsASnapshot(t *testing.T) {
	ctx := context.Background()
	f := New()
	if err := f.Start(ctx, "s", []runtime.Window{{Name: "a"}}); err != nil {
		t.Fatal(err)
	}
	before, _ := f.State(ctx, "s")
	if err := f.Start(ctx, "s", []runtime.Window{{Name: "b"}}); err != nil {
		t.Fatal(err)
	}
	if _, ok := before.Pane("b"); ok {
		t.Fatal("a later window reached an earlier snapshot")
	}
	before.Panes["c"] = runtime.PaneState{}
	if after, _ := f.State(ctx, "s"); len(after.Panes) != 2 {
		t.Fatalf("a snapshot reached the fake: %+v", after)
	}
}

func TestFailuresAreIndependent(t *testing.T) {
	ctx := context.Background()
	f := New()
	f.Seed("s", map[string]runtime.PaneState{"a": {}})
	boom := errors.New("boom")
	f.Fail(OpState, boom)
	if ok, err := f.HasSession(ctx, "s"); err != nil || !ok {
		t.Fatalf("HasSession = %v, %v", ok, err)
	}
	if _, err := f.State(ctx, "s"); !errors.Is(err, boom) {
		t.Fatalf("State err = %v", err)
	}
	f.Fail(OpState, nil)
	if _, err := f.State(ctx, "s"); err != nil {
		t.Fatal(err)
	}
}

func TestExit(t *testing.T) {
	ctx := context.Background()
	f := New()
	if err := f.Start(ctx, "s", []runtime.Window{{Name: "a"}}); err != nil {
		t.Fatal(err)
	}
	if err := f.OpenWindow(ctx, "s", runtime.Window{Name: "t", Transient: true}); err != nil {
		t.Fatal(err)
	}
	f.Exit("s", "a")
	f.Exit("s", "t")
	st, _ := f.State(ctx, "s")
	if !st.Panes["a"].Dead {
		t.Fatalf("managed window not held dead: %+v", st)
	}
	if _, ok := st.Pane("t"); ok {
		t.Fatalf("transient window held: %+v", st)
	}
}

func TestRebootEndsEverySession(t *testing.T) {
	ctx := context.Background()
	f := New()
	f.Seed("a", map[string]runtime.PaneState{"w": {}})
	f.Seed("b", map[string]runtime.PaneState{"w": {}})
	f.Reboot()
	for _, s := range []string{"a", "b"} {
		if ok, err := f.HasSession(ctx, s); err != nil || ok {
			t.Fatalf("%s after reboot: %v, %v", s, ok, err)
		}
	}
}

func TestCallsRecordWhatRan(t *testing.T) {
	ctx := context.Background()
	f := New()
	w := runtime.Window{Name: "a", Command: []string{"false"}}
	if err := f.Start(ctx, "s", []runtime.Window{w}); err != nil {
		t.Fatal(err)
	}
	if err := f.Start(ctx, "s", []runtime.Window{w}); err != nil {
		t.Fatal(err)
	}
	if err := f.Restart(ctx, "s", w); err != nil {
		t.Fatal(err)
	}
	if err := f.Stop(ctx, "s"); err != nil {
		t.Fatal(err)
	}
	if got := len(f.Calls(OpStart)); got != 1 {
		t.Fatalf("Start ran %d windows, want 1 from the two calls", got)
	}
	if got := fmt.Sprint(len(f.Calls(OpRestart, OpStop)), len(f.Calls())); got != "2 3" {
		t.Fatalf("calls = %s", got)
	}
}
