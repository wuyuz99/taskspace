package cli

import (
	"context"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// start and clean of one task serialize: clean waits while start's
// post_create hook runs, and acts only after start is done. The hook itself
// may update the task, but not clean it.
func TestStartAndCleanSerialize(t *testing.T) {
	h := newHarness(t)
	dir := t.TempDir()
	ready, release, log := filepath.Join(dir, "ready"), filepath.Join(dir, "release"), filepath.Join(dir, "hook.log")
	hook := `"$TSPACE_BIN" task update PAY-1 --title from-hook >/dev/null 2>>"$HOOK_LOG"; echo "update $?" >>"$HOOK_LOG"
"$TSPACE_BIN" clean PAY-1 >/dev/null 2>>"$HOOK_LOG"; echo "clean $?" >>"$HOOK_LOG"
: >"$HOOK_READY"
while [ ! -f "$RELEASE" ]; do sleep 0.05; done`
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-1", "--repo", "backend="+h.repo); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newRepoCmd(), "update", "PAY-1", "backend", "--post-create", hook); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(), "TSPACE_TEST_CLI=1", "TSPACE_ROOT="+h.app.Store.Root(),
		"TSPACE_MULTIPLEXER=tspace-test-none", "TSPACE_BIN="+self,
		"HOOK_READY="+ready, "RELEASE="+release, "HOOK_LOG="+log)
	// Stderr goes to a file, which can be read while the process runs.
	run := func(args ...string) (*osexec.Cmd, func() string) {
		path := filepath.Join(dir, args[0]+".stderr")
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { f.Close() })
		c := osexec.Command(self, args...)
		c.Env = env
		c.Stderr = f
		if err := c.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Process.Kill(); _ = c.Wait() })
		return c, func() string { b, _ := os.ReadFile(path); return string(b) }
	}
	waitFor := func(what string, done func() bool) {
		t.Helper()
		for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
			if done() {
				return
			}
		}
		var seen strings.Builder
		for _, name := range []string{"start.stderr", "clean.stderr", "hook.log"} {
			b, _ := os.ReadFile(filepath.Join(dir, name))
			fmt.Fprintf(&seen, "\n%s:\n%s", name, b)
		}
		t.Fatalf("timed out waiting for %s%s", what, seen.String())
	}

	start, startErr := run("start", "PAY-1", "--no-runtime")
	waitFor("the hook", func() bool { _, err := os.Stat(ready); return err == nil })
	clean, cleanErr := run("clean", "PAY-1")
	waitFor("clean to wait", func() bool { return strings.Contains(cleanErr(), "waiting for") })
	worktree := h.app.Store.WorktreePath("PAY-1", "backend")
	if _, err := os.Stat(worktree); err != nil {
		t.Fatalf("clean acted while start held the task: %v", err)
	}

	if err := os.WriteFile(release, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := start.Wait(); err != nil {
		t.Fatalf("start: %v: %s", err, startErr())
	}
	if err := clean.Wait(); err != nil {
		t.Fatalf("clean: %v: %s", err, cleanErr())
	}
	if _, err := os.Stat(worktree); !os.IsNotExist(err) {
		t.Fatalf("clean did not remove the worktree after start: %v", err)
	}

	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	hookLog := string(b)
	if !strings.Contains(hookLog, "update 0") || !strings.Contains(hookLog, "clean 1") ||
		!strings.Contains(hookLog, "cannot run from a hook") {
		t.Fatalf("hook log:\n%s", hookLog)
	}
	// start saved after the hook without losing what the hook changed.
	if got := h.load(t, "PAY-1").Title; got != "from-hook" {
		t.Fatalf("title %q: the hook's update was lost", got)
	}
}

// Reading a task never waits for a command changing it, and shows a legacy
// record's inline notes as they are.
func TestReadsDoNotWaitForTheLock(t *testing.T) {
	h := newHarness(t)
	// A record from before versions, its handoff inline.
	if err := os.MkdirAll(h.app.Store.TasksDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := `{"name": "PAY-1", "handoff": "inline handoff"}`
	if err := os.WriteFile(filepath.Join(h.app.Store.TasksDir(), "PAY-1.json"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := h.app.Store.Lock(context.Background(), "PAY-1", "busy", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Unlock()

	done := make(chan error, 1)
	go func() {
		if err := h.exec(t, h.app.newTaskCmd(), "handoff", "PAY-1"); err != nil {
			done <- err
			return
		}
		out := h.out.String()
		if err := h.exec(t, h.app.newTaskCmd(), "show", "PAY-1"); err != nil {
			done <- err
			return
		}
		if !strings.Contains(out, "inline handoff") {
			done <- fmt.Errorf("handoff printed %q", out)
			return
		}
		done <- nil
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a read waited for the lock")
	}
	if got, err := h.app.Store.Load("PAY-1"); err != nil || got.LegacyHandoff == "" {
		t.Fatalf("a read migrated the record: %+v, %v", got, err)
	}
}

// A post_create hook may change the task while start runs it, and start
// launches what the hook left, not what it loaded first.
func TestStartUsesWhatItsHookChanged(t *testing.T) {
	h := newHarness(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TSPACE_TEST_CLI", "1")
	t.Setenv("TSPACE_ROOT", h.app.Store.Root())
	t.Setenv("TSPACE_MULTIPLEXER", "tspace-test-none")
	t.Setenv("TSPACE_BIN", self)
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-1", "--with-root", "--repo", "backend="+h.repo); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newRepoCmd(), "update", "PAY-1", "backend",
		"--post-create", `"$TSPACE_BIN" repo update PAY-1 backend --agent hook-agent`); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newStartCmd(), "PAY-1"); err != nil {
		t.Fatalf("start: %v\n%s", err, h.errb.String())
	}
	rt := h.app.Runtime.(*stubRuntime)
	for _, w := range rt.started {
		if w.Name == "backend" {
			if len(w.Command) == 0 || w.Command[len(w.Command)-1] != "hook-agent" {
				t.Fatalf("backend launched %v, not the hook's agent", w.Command)
			}
			return
		}
	}
	t.Fatalf("backend not started: %+v", rt.started)
}

// create --start runs start under create's lock; a hook start runs still
// inherits it, rather than waiting on create while create waits on it.
func TestCreateStartHookInheritsTheLock(t *testing.T) {
	h := newHarness(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TSPACE_TEST_CLI", "1")
	t.Setenv("TSPACE_ROOT", h.app.Store.Root())
	t.Setenv("TSPACE_MULTIPLEXER", "tspace-test-none")
	t.Setenv("TSPACE_BIN", self)
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-1", "--repo", "backend="+h.repo); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newRepoCmd(), "update", "PAY-1", "backend",
		"--post-create", `"$TSPACE_BIN" task update "$TSPACE_TASK" --title from-hook`); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newProjectCmd(), "save", "recipe", "--from", "PAY-1"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := h.app.newTaskCmd()
	cmd.SetArgs([]string{"create", "PAY-2", "--project", "recipe", "--start"})
	cmd.SetOut(h.out)
	cmd.SetErr(h.errb)
	if err := cmd.ExecuteContext(ctx); err != nil {
		t.Fatalf("create --start: %v\n%s", err, h.errb.String())
	}
	if got := h.load(t, "PAY-2").Title; got != "from-hook" {
		t.Fatalf("title %q; stderr:\n%s", got, h.errb.String())
	}
}

// dash cannot show a wait or stop one, so its commands fail on a busy task.
func TestNoWaitFailsOnABusyTask(t *testing.T) {
	h := newHarness(t)
	if _, err := h.app.Store.Create("PAY-1"); err != nil {
		t.Fatal(err)
	}
	l, err := h.app.Store.Lock(context.Background(), "PAY-1", "tspace start PAY-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Unlock()
	h.app.noWait = true
	err = h.exec(t, h.app.newTaskCmd(), "update", "PAY-1", "--title", "x")
	if err == nil || !strings.Contains(err.Error(), "PAY-1 is busy") || !strings.Contains(err.Error(), "tspace start PAY-1") {
		t.Fatalf("update of a busy task = %v", err)
	}
}
