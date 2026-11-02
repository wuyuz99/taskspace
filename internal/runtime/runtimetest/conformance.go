package runtimetest

import (
	"context"
	"testing"
	"time"

	"github.com/wuyuz99/taskspace/internal/runtime"
)

// Target is a multiplexer for Conformance to check.
type Target struct {
	Runtime runtime.Runtime
	// Session names a session for one case, unused by anything else.
	Session func(t *testing.T) string
	// NeedsClient says a window can only be added with a client attached, which
	// a restart with another command does too.
	NeedsClient bool
	// KeepsEmptySession says closing the last window leaves the session.
	KeepsEmptySession bool
}

// Windows that Conformance runs on every target.
var (
	alive  = []string{"sleep", "60"}
	exited = []string{"false"}
)

// Conformance checks the behaviour tspace relies on, so that tests using the
// fake mean the same on a real multiplexer. target is called once per case.
func Conformance(t *testing.T, target func(t *testing.T) Target) {
	ctx := context.Background()
	cases := []struct {
		name string
		skip func(Target) bool
		run  func(t *testing.T, tg Target, session, dir string)
	}{
		{name: "states match state", skip: cannotList, run: func(t *testing.T, tg Target, s, dir string) {
			lister := tg.Runtime.(runtime.StateLister)
			all, err := lister.States(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := all[s]; ok {
				t.Fatalf("%s is listed before it starts", s)
			}
			start(t, tg, s, runtime.Window{Name: "_root", Dir: dir, Command: alive})
			all, err = lister.States(ctx)
			if err != nil {
				t.Fatal(err)
			}
			got, ok := all[s]
			if !ok || got.Err != nil || !got.State.Exists {
				t.Fatalf("States[%s] = %+v, listed %v", s, got, ok)
			}
			if _, ok := got.State.Pane("_root"); !ok {
				t.Fatalf("panes: %+v", got.State.Panes)
			}
		}},
		{name: "start refuses no windows", run: func(t *testing.T, tg Target, s, _ string) {
			if err := tg.Runtime.Start(ctx, s, nil); err == nil {
				t.Fatal("started a session with no windows")
			}
		}},
		{name: "no session", run: func(t *testing.T, tg Target, s, _ string) {
			st, err := tg.Runtime.State(ctx, s)
			if err != nil || st.Exists || len(st.Panes) != 0 {
				t.Fatalf("State = %+v, %v", st, err)
			}
			if ok, err := tg.Runtime.HasSession(ctx, s); err != nil || ok {
				t.Fatalf("HasSession = %v, %v", ok, err)
			}
			if err := tg.Runtime.Stop(ctx, s); err != nil {
				t.Fatalf("Stop: %v", err)
			}
			if err := tg.Runtime.Restart(ctx, s, runtime.Window{Name: "w"}); err == nil {
				t.Fatal("restarted a window with no session")
			}
		}},
		{name: "start shows every window at once", run: func(t *testing.T, tg Target, s, dir string) {
			start(t, tg, s, runtime.Window{Name: "_root", Dir: dir, Command: alive},
				runtime.Window{Name: "api.v1", Dir: dir, Command: alive})
			st := state(t, tg, s)
			for _, name := range []string{"_root", "api.v1"} {
				if _, ok := st.Pane(name); !ok {
					t.Fatalf("%s missing right after Start: %+v", name, st)
				}
			}
		}},
		{name: "second start adds only what is missing", run: func(t *testing.T, tg Target, s, dir string) {
			root := runtime.Window{Name: "_root", Dir: dir, Command: alive}
			// A second start must leave this alone, not rerun it as `false`.
			start(t, tg, s, root)
			again := root
			again.Command = exited
			windows := []runtime.Window{again}
			if !tg.NeedsClient {
				windows = append(windows, runtime.Window{Name: "api", Dir: dir, Command: alive})
			}
			start(t, tg, s, windows...)
			st := state(t, tg, s)
			if _, ok := st.Pane("api"); !ok && !tg.NeedsClient {
				t.Fatal("missing window not added")
			}
			if st.Panes["_root"].Dead {
				t.Fatalf("running window replaced: %+v", st)
			}
		}},
		{name: "a finished command leaves a dead window", run: func(t *testing.T, tg Target, s, dir string) {
			start(t, tg, s, runtime.Window{Name: "_root", Dir: dir, Command: alive},
				runtime.Window{Name: "api", Dir: dir, Command: exited})
			waitFor(t, tg, s, "window held dead", func(st runtime.SessionState) bool {
				p, ok := st.Pane("api")
				return ok && p.Dead && !st.Panes["_root"].Dead
			})
		}},
		{name: "restart only relaunches a dead window", run: func(t *testing.T, tg Target, s, dir string) {
			root := runtime.Window{Name: "_root", Dir: dir, Command: alive}
			api := runtime.Window{Name: "api", Dir: dir, Command: exited}
			start(t, tg, s, root, api)
			waitFor(t, tg, s, "window held dead", func(st runtime.SessionState) bool { return st.Panes["api"].Dead })
			if err := tg.Runtime.Restart(ctx, s, root); err == nil {
				t.Fatal("live window restarted")
			}
			if err := tg.Runtime.Restart(ctx, s, runtime.Window{Name: "gone", Dir: dir, Command: alive}); err == nil {
				t.Fatal("missing window restarted")
			}
			if err := tg.Runtime.Restart(ctx, s, api); err != nil {
				t.Fatal(err)
			}
			if tg.NeedsClient {
				return
			}
			api.Command = alive
			waitFor(t, tg, s, "window dead again", func(st runtime.SessionState) bool { return st.Panes["api"].Dead })
			if err := tg.Runtime.Restart(ctx, s, api); err != nil {
				t.Fatal(err)
			}
			waitFor(t, tg, s, "window rerun", func(st runtime.SessionState) bool {
				p, ok := st.Pane("api")
				return ok && !p.Dead
			})
		}},
		{name: "open refuses a window that exists", skip: needsClient, run: func(t *testing.T, tg Target, s, dir string) {
			w := runtime.Window{Name: "_root", Dir: dir, Command: alive}
			start(t, tg, s, w)
			if err := tg.Runtime.OpenWindow(ctx, s, w); err == nil {
				t.Fatal("window opened twice")
			}
			if err := tg.Runtime.OpenWindow(ctx, s+"-none", w); err == nil {
				t.Fatal("window opened with no session")
			}
		}},
		{name: "a transient window closes with its command", skip: needsClient, run: func(t *testing.T, tg Target, s, dir string) {
			start(t, tg, s, runtime.Window{Name: "_root", Dir: dir, Command: alive})
			w := runtime.Window{Name: "_x", Dir: dir, Command: exited, Transient: true}
			if err := tg.Runtime.OpenWindow(ctx, s, w); err != nil {
				t.Fatal(err)
			}
			waitFor(t, tg, s, "window closed", func(st runtime.SessionState) bool {
				_, open := st.Pane("_x")
				return st.Exists && !open
			})
		}},
		{name: "rename keeps the window", skip: needsClient, run: func(t *testing.T, tg Target, s, dir string) {
			start(t, tg, s, runtime.Window{Name: "_root", Dir: dir, Command: alive})
			if err := tg.Runtime.OpenWindow(ctx, s, runtime.Window{Name: "_old", Dir: dir, Command: alive}); err != nil {
				t.Fatal(err)
			}
			if err := tg.Runtime.RenameWindow(ctx, s, "_old", "_new"); err != nil {
				t.Fatal(err)
			}
			st := state(t, tg, s)
			if _, ok := st.Pane("_new"); !ok {
				t.Fatalf("not renamed: %+v", st)
			}
			if _, ok := st.Pane("_old"); ok {
				t.Fatalf("old name left: %+v", st)
			}
		}},
		{name: "closing a window leaves the rest", run: func(t *testing.T, tg Target, s, dir string) {
			start(t, tg, s, runtime.Window{Name: "_root", Dir: dir, Command: alive},
				runtime.Window{Name: "api", Dir: dir, Command: alive})
			c := closer(t, tg)
			if err := c.CloseWindow(ctx, s, "api"); err != nil {
				t.Fatal(err)
			}
			waitFor(t, tg, s, "window closed", func(st runtime.SessionState) bool {
				_, open := st.Pane("api")
				_, root := st.Pane("_root")
				return st.Exists && !open && root
			})
			if err := c.CloseWindow(ctx, s, "api"); err != nil {
				t.Fatalf("closing a closed window: %v", err)
			}
		}},
		{name: "a session ends with its last window", skip: func(tg Target) bool { return tg.KeepsEmptySession },
			run: func(t *testing.T, tg Target, s, dir string) {
				start(t, tg, s, runtime.Window{Name: "_root", Dir: dir, Command: alive})
				if err := closer(t, tg).CloseWindow(ctx, s, "_root"); err != nil {
					t.Fatal(err)
				}
				waitFor(t, tg, s, "session ended", func(st runtime.SessionState) bool { return !st.Exists })
			}},
		{name: "stop ends the session", run: func(t *testing.T, tg Target, s, dir string) {
			start(t, tg, s, runtime.Window{Name: "_root", Dir: dir, Command: alive})
			if err := tg.Runtime.Stop(ctx, s); err != nil {
				t.Fatal(err)
			}
			if ok, err := tg.Runtime.HasSession(ctx, s); err != nil || ok {
				t.Fatalf("after Stop: %v, %v", ok, err)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tg := target(t)
			if c.skip != nil && c.skip(tg) {
				t.Skip("not for this multiplexer")
			}
			c.run(t, tg, tg.Session(t), t.TempDir())
		})
	}
}

func needsClient(tg Target) bool { return tg.NeedsClient }

func cannotList(tg Target) bool {
	_, ok := tg.Runtime.(runtime.StateLister)
	return !ok
}

func start(t *testing.T, tg Target, session string, windows ...runtime.Window) {
	t.Helper()
	if err := tg.Runtime.Start(context.Background(), session, windows); err != nil {
		t.Fatal(err)
	}
}

func state(t *testing.T, tg Target, session string) runtime.SessionState {
	t.Helper()
	st, err := tg.Runtime.State(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func closer(t *testing.T, tg Target) runtime.WindowCloser {
	t.Helper()
	c, ok := tg.Runtime.(runtime.WindowCloser)
	if !ok {
		t.Fatalf("%T cannot close a window", tg.Runtime)
	}
	return c
}

// waitFor polls, because a real multiplexer sees a command exit a moment later.
func waitFor(t *testing.T, tg Target, session, what string, ok func(runtime.SessionState) bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		st := state(t, tg, session)
		if ok(st) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: %+v", what, st)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
