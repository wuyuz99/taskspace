package task

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMain lets the test binary be a second tspace: locks are between
// processes, so the tests race real ones.
func TestMain(m *testing.M) {
	if mode := os.Getenv("TSPACE_TEST_LOCK_HELPER"); mode != "" {
		os.Exit(lockHelper(mode))
	}
	os.Exit(m.Run())
}

// lockHelper does one locked operation on TSPACE_TEST_ROOT's task "t". It
// touches $READY once it holds the lock, if it holds one, and waits for
// $RELEASE before letting go.
func lockHelper(mode string) int {
	s := NewStore(os.Getenv("TSPACE_TEST_ROOT"))
	ctx := context.Background()
	ready := func() { _ = os.WriteFile(os.Getenv("READY"), nil, 0o644) }
	release := func() {
		for {
			if _, err := os.Stat(os.Getenv("RELEASE")); err == nil {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	var err error
	switch mode {
	case "update-title":
		_, err = s.Update(ctx, "t", "test", func(t *Task) error {
			ready()
			release()
			t.Title = "from A"
			return nil
		})
	case "update-ticket":
		_, err = s.Update(ctx, "t", "test", func(t *Task) error {
			t.Ticket = "from B"
			return nil
		})
	case "create":
		_, err = s.Create("t")
	case "delete":
		var l *Lock
		if l, err = s.Lock(ctx, "t", "delete", nil); err == nil {
			err = s.Delete("t")
			ready()
			release()
			l.Unlock()
		}
	case "hold":
		var l *Lock
		if l, err = s.Lock(ctx, "t", "hold", nil); err == nil {
			ready()
			release()
			l.Unlock() // and holds l until here: a collected file closes, and unlocks
		}
	}
	switch {
	case errors.Is(err, ErrExists), errors.Is(err, ErrNotFound):
		fmt.Fprintln(os.Stderr, err)
		return 2
	case err != nil:
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

type helper struct {
	cmd           *exec.Cmd
	ready, stderr string
}

func startHelper(t *testing.T, s *Store, mode, release string) *helper {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	h := &helper{ready: filepath.Join(dir, "ready"), stderr: filepath.Join(dir, "stderr")}
	errf, err := os.Create(h.stderr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { errf.Close() })
	h.cmd = exec.Command(self)
	h.cmd.Env = append(os.Environ(), "TSPACE_TEST_LOCK_HELPER="+mode, "TSPACE_TEST_ROOT="+s.Root(),
		"READY="+h.ready, "RELEASE="+release)
	h.cmd.Stderr = errf
	if err := h.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.cmd.Process.Kill(); _ = h.cmd.Wait() })
	return h
}

func (h *helper) waitReady(t *testing.T) {
	t.Helper()
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if _, err := os.Stat(h.ready); err == nil {
			return
		}
	}
	t.Fatal("helper never took the lock")
}

// exit waits for the helper and returns its exit code.
func (h *helper) exit(t *testing.T) int {
	t.Helper()
	err := h.cmd.Wait()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode()
	}
	if err != nil {
		t.Fatal(err)
	}
	return 0
}

func (h *helper) errText() string {
	b, _ := os.ReadFile(h.stderr)
	return strings.TrimSpace(string(b))
}

func releaser(t *testing.T) (path string, release func()) {
	path = filepath.Join(t.TempDir(), "release")
	return path, func() {
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// Two processes updating different fields of one task both keep their
// change: B loads only once A has saved.
func TestConcurrentUpdatesKeepBothChanges(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Create("t"); err != nil {
		t.Fatal(err)
	}
	path, release := releaser(t)
	a := startHelper(t, s, "update-title", path)
	a.waitReady(t)
	b := startHelper(t, s, "update-ticket", path)
	// Without the lock, B would load, save and exit while A still waits, and A
	// would then save over B's ticket.
	time.Sleep(300 * time.Millisecond)
	release()
	if code := a.exit(t); code != 0 {
		t.Fatalf("A exited %d: %s", code, a.errText())
	}
	if code := b.exit(t); code != 0 {
		t.Fatalf("B exited %d: %s", code, b.errText())
	}
	got, err := s.Load("t")
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "from A" || got.Ticket != "from B" {
		t.Fatalf("title %q, ticket %q: a change was lost", got.Title, got.Ticket)
	}
}

func TestConcurrentCreateHasOneWinner(t *testing.T) {
	s := newTestStore(t)
	path, release := releaser(t)
	release()
	var helpers []*helper
	for range 6 {
		helpers = append(helpers, startHelper(t, s, "create", path))
	}
	won := 0
	for _, h := range helpers {
		switch code := h.exit(t); code {
		case 0:
			won++
		case 2:
		default:
			t.Fatalf("create exited %d: %s", code, h.errText())
		}
	}
	if won != 1 {
		t.Fatalf("%d creates won, want 1", won)
	}
}

// A delete that comes while an update is under way waits for it, so the
// update's save cannot bring the deleted task back; an update that comes
// after finds the task gone.
func TestUpdateAndDeleteDoNotResurrect(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Create("t"); err != nil {
		t.Fatal(err)
	}
	path, release := releaser(t)
	upd := startHelper(t, s, "update-title", path)
	upd.waitReady(t)
	del := startHelper(t, s, "delete", path)
	// Without the lock, the delete would land now, and the update's save
	// after it would recreate the record.
	time.Sleep(300 * time.Millisecond)
	release()
	if code := upd.exit(t); code != 0 {
		t.Fatalf("update exited %d: %s", code, upd.errText())
	}
	if code := del.exit(t); code != 0 {
		t.Fatalf("delete exited %d: %s", code, del.errText())
	}
	if s.Exists("t") {
		t.Fatal("the deleted task came back")
	}
	if code := startHelper(t, s, "update-ticket", path).exit(t); code != 2 {
		t.Fatalf("update of a deleted task exited %d, want not found", code)
	}
	if s.Exists("t") {
		t.Fatal("an update recreated the deleted task")
	}
}

// A lock goes with its process however that ends, and waiting for it stops
// with the context, without touching the holder's lock.
func TestLockIsReleasedWhenItsHolderDies(t *testing.T) {
	s := newTestStore(t)
	path, _ := releaser(t)
	h := startHelper(t, s, "hold", path)
	h.waitReady(t)

	var holder LockHolder
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := s.Lock(ctx, "t", "test", func(lh LockHolder) { holder = lh })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Lock while held = %v", err)
	}
	if holder.PID != h.cmd.Process.Pid || holder.Command != "hold" {
		t.Errorf("waiting saw holder %+v", holder)
	}
	// Different tasks are independent.
	other, err := s.Lock(context.Background(), "u", "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	other.Unlock()

	if err := h.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = h.cmd.Wait()
	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	l, err := s.Lock(ctx, "t", "test", nil)
	if err != nil {
		t.Fatalf("lock of a dead holder not released: %v", err)
	}
	l.Unlock()
	if _, err := os.Stat(s.LockPath("t")); err != nil {
		t.Errorf("lock file removed: %v", err)
	}
}

// Taking a lock again is free with the context that carries it. Another
// command of the same process, as in dash, waits for it like any other.
func TestNestedLocks(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Create("t"); err != nil {
		t.Fatal(err)
	}
	l, err := s.Lock(context.Background(), "t", "outer", nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithLock(context.Background(), l)
	if _, err := s.Update(ctx, "t", "nested", func(t *Task) error { t.Title = "nested"; return nil }); err != nil {
		t.Fatalf("nested Update: %v", err)
	}
	var waitedFor LockHolder
	done := make(chan error, 1)
	go func() {
		other, err := s.Lock(context.Background(), "t", "other", func(h LockHolder) { waitedFor = h })
		if err == nil {
			other.Unlock()
		}
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("a second command of this process did not wait: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	l.Unlock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the second command never got the lock")
	}
	if waitedFor.Command != "outer" {
		t.Errorf("waited for %+v", waitedFor)
	}
	// A released lock is no longer carried: the context does not re-enter it.
	if s.HeldLock(ctx, "t") != nil {
		t.Fatal("a released lock still re-enters")
	}
	// A done context makes one attempt, and does not wait.
	l, err = s.Lock(context.Background(), "t", "outer", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Unlock()
	try, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Lock(try, "t", "try", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("Lock with a done context = %v", err)
	}
}

// A process the holder waits for inherits the lock through $TSPACE_LOCKED,
// and only while that holder has it.
func TestInheritedLock(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Create("t"); err != nil {
		t.Fatal(err)
	}
	path, release := releaser(t)
	h := startHelper(t, s, "hold", path)
	h.waitReady(t)

	t.Setenv(EnvLocked, "u:x,t:"+readHolder(s.LockPath("t")).token)
	l, err := s.Lock(context.Background(), "t", "hook", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !l.Inherited() {
		t.Fatal("lock not inherited")
	}
	l.Unlock()

	// A value from another acquisition is not this one's.
	t.Setenv(EnvLocked, "t:0123456789abcdef")
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, err := s.Lock(ctx, "t", "hook", nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("stale TSPACE_LOCKED = %v", err)
	}
	release()
	_ = h.exit(t)
}
