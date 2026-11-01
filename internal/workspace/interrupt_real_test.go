package workspace

import (
	"context"
	"fmt"
	"os"
	osexec "os/exec"
	"slices"
	"testing"
	"time"

	tsexec "github.com/wuyuz99/taskspace/internal/exec"
	"github.com/wuyuz99/taskspace/internal/runtime"
	"github.com/wuyuz99/taskspace/internal/task"
)

// A real interruption: a root window and a repository alive, one exited, one
// closed on purpose and a saved window open. Only what was alive comes back,
// with no client attached, which zellij needs to add to a session later.
func interruption(t *testing.T, rt runtime.Runtime, name string, kill func(session string)) {
	t.Helper()
	f := newFixture(t)
	f.svc.Runtime = rt
	f.svc.Settle = 500 * time.Millisecond
	sleep := []string{"sleep", "60"}
	tk := f.task(t, name, map[string]task.Repo{
		"a": {Path: gitRepo(t), Agent: sleep},
		"b": {Path: gitRepo(t), Agent: []string{"false"}},
		"c": {Path: gitRepo(t), Agent: sleep},
	})
	tk.Agent = sleep
	tk.Windows = []task.Window{{Name: "logs", Command: sleep}}
	saveTask(t, f, tk)
	ctx := context.Background()
	session := rt.SessionName(name)
	if _, err := f.svc.Start(ctx, name, StartOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.StopWindow(ctx, name, "c", false); err != nil {
		t.Fatal(err)
	}
	if r, _ := f.svc.Store.Running(name); !r.Root || !slices.Equal(r.Repos, []string{"a"}) || !slices.Equal(r.Windows, []string{"logs"}) {
		t.Fatalf("snapshot %+v", r)
	}

	kill(session)
	deadline := time.Now().Add(5 * time.Second)
	for {
		names, err := f.svc.Interrupted(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if slices.Equal(names, []string{name}) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("interrupted %v", names)
		}
		time.Sleep(50 * time.Millisecond)
	}

	started, failed, err := f.svc.StartInterrupted(ctx, StartOptions{})
	if err != nil || len(failed) > 0 || !slices.Equal(started, []string{name}) {
		t.Fatalf("started %v, failed %v, %v", started, failed, err)
	}
	st, err := rt.State(ctx, session)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{task.RootWindowName, "a", "_logs"} {
		if p, ok := st.Pane(rt.WindowName(w)); !ok || p.Dead {
			t.Errorf("%s did not come back: %+v", w, st)
		}
	}
	for _, w := range []string{"b", "c"} {
		if _, ok := st.Pane(rt.WindowName(w)); ok {
			t.Errorf("%s came back: %+v", w, st)
		}
	}
	if _, err := f.svc.Stop(ctx, name); err != nil {
		t.Fatal(err)
	}
	if f.svc.Store.Interrupted(name) {
		t.Error("stopped on purpose, and still interrupted")
	}
}

func TestRealTmuxInterruption(t *testing.T) {
	if _, err := osexec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}
	// Socket length is limited on Unix; Go's per-test directory can be long.
	dir, err := os.MkdirTemp("", "tspace-int-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	t.Setenv("TMUX_TMPDIR", dir)
	t.Setenv("TMUX", "")
	t.Cleanup(func() { osexec.Command("tmux", "kill-server").Run() })
	rt := runtime.NewTmux(tsexec.Real())
	rt.StopGrace = 0
	interruption(t, rt, "interrupted", func(string) {
		if out, err := osexec.Command("tmux", "kill-server").CombinedOutput(); err != nil {
			t.Fatalf("kill-server: %v\n%s", err, out)
		}
	})
}

// zellij keeps a killed session listed as exited, as it does across a computer
// restart, so that is what recovery has to read as gone.
func TestRealZellijInterruption(t *testing.T) {
	if _, err := osexec.LookPath("zellij"); err != nil {
		t.Skip("zellij unavailable")
	}
	rt := runtime.NewZellij(tsexec.Real())
	rt.StopGrace = 0
	if err := rt.Available(context.Background()); err != nil {
		t.Skip(err)
	}
	t.Setenv("ZELLIJ", "")
	name := fmt.Sprintf("zi-%d", os.Getpid())
	session := rt.SessionName(name)
	t.Cleanup(func() {
		osexec.Command("zellij", "kill-session", session).Run()
		osexec.Command("zellij", "delete-session", session).Run()
	})
	interruption(t, rt, name, func(session string) {
		if out, err := osexec.Command("zellij", "kill-session", session).CombinedOutput(); err != nil {
			t.Fatalf("kill-session: %v\n%s", err, out)
		}
	})
}
