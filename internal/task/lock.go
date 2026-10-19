package task

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Task locks serialize everything that changes one task: its record, its
// worktrees and its session. Writes of the record are atomic on their own, but
// a command loads the record, works, and saves it, and two commands doing that
// at once lose one's changes; start and clean at once fight over the worktrees.
//
// The rules:
//
//   - A command that changes a task takes the task's lock before it loads the
//     record, and holds it until it is done, so what it saves was read under
//     the lock. Tasks have a lock each; different tasks never wait for each
//     other. Reading needs no lock: records are replaced atomically.
//   - The lock is an advisory OS lock (flock, or LockFileEx on Windows) on
//     tasks/.locks/TASK.lock. The OS drops it when its process exits however
//     it exits, so there is never a stale lock to clean up, and lock files are
//     never deleted: removing one while another process waits on it would let
//     two processes hold "the" lock.
//   - A waiting command says who holds the lock and waits until it is free or
//     its context is cancelled (Ctrl-C).
//   - Within a process the lock travels in the context (WithLock): code that
//     already holds it, such as `task create --start` running start, takes it
//     again for free. Taking it again without that context is a bug, and fails
//     at once rather than waiting on itself forever.
//   - A process the holder runs and waits for, such as a post_create hook, gets
//     the lock in $TSPACE_LOCKED and may change the record, which the holder
//     reloads before saving its own changes. It may not run commands that take
//     the lock to change worktrees or the session (Inherited reports that): the
//     holder is partway through doing that itself.
//   - Nobody holds a lock while attached to a terminal: open releases it
//     before it attaches.
type Lock struct {
	store *Store
	name  string
	// f is the locked file; nil for a lock this one re-enters or inherits.
	f         *os.File
	inherited bool
	once      sync.Once
}

// EnvLocked passes the task locks held by the processes a process was run by,
// and waits on, to it: TASK:PID entries separated by commas.
const EnvLocked = "TSPACE_LOCKED"

// lockPoll is how often a waiting command tries again.
const lockPoll = 50 * time.Millisecond

// ErrLockedHere is returned when a process asks for a lock it already holds
// without the context that carries it.
var ErrLockedHere = errors.New("already locked by this process")

type lockKey struct{ root, name string }

// WithLock returns ctx carrying l, so code given ctx re-enters l.
func WithLock(ctx context.Context, l *Lock) context.Context {
	return context.WithValue(ctx, lockKey{l.store.root, l.name}, l)
}

// HeldLock is the lock on name that ctx carries, or nil.
func (s *Store) HeldLock(ctx context.Context, name string) *Lock {
	l, _ := ctx.Value(lockKey{s.root, name}).(*Lock)
	return l
}

// Inherited reports whether the lock is held by the process that ran this
// one, such as the start running a post_create hook.
func (l *Lock) Inherited() bool { return l.inherited }

// Env is the variable that hands the lock to a process its holder waits for,
// along with the locks this process inherited: they are all held by processes
// waiting on that one.
func (l *Lock) Env() string {
	held := os.Getenv(EnvLocked)
	if l.inherited {
		return EnvLocked + "=" + held
	}
	mine := l.name + ":" + strconv.Itoa(os.Getpid())
	if held != "" {
		mine = held + "," + mine
	}
	return EnvLocked + "=" + mine
}

// Unlock releases the lock. It is safe to call more than once; for a lock
// re-entered or inherited it does nothing.
func (l *Lock) Unlock() {
	l.once.Do(func() {
		if l.f != nil {
			_ = unlockFile(l.f)
			_ = l.f.Close()
		}
	})
}

// LockPath is the file a task's lock is held on.
func (s *Store) LockPath(name string) string {
	return filepath.Join(s.TasksDir(), ".locks", name+".lock")
}

// LockHolder describes the process holding a lock, as far as it said.
type LockHolder struct {
	PID     int
	Command string
	Since   time.Time
}

func (h LockHolder) String() string {
	if h.PID == 0 {
		return "another tspace"
	}
	s := fmt.Sprintf("pid %d", h.PID)
	if h.Command != "" {
		s += " (" + h.Command + ")"
	}
	if !h.Since.IsZero() {
		s += ", since " + h.Since.Local().Format("15:04:05")
	}
	return s
}

// Lock takes the task's lock for command, which is recorded for anyone who
// has to wait. If the lock is busy, waiting is called once with its holder,
// and Lock tries again until it is free or ctx is done.
func (s *Store) Lock(ctx context.Context, name, command string, waiting func(LockHolder)) (*Lock, error) {
	if err := ValidateName("task", name); err != nil {
		return nil, err
	}
	if held := s.HeldLock(ctx, name); held != nil {
		return &Lock{store: s, name: name, inherited: held.inherited}, nil
	}
	path := s.LockPath(name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock for task %q: %w", name, err)
	}
	told := false
	for {
		ok, err := tryLockFile(f)
		if err != nil {
			f.Close()
			return nil, fmt.Errorf("lock task %q: %w", name, err)
		}
		if ok {
			break
		}
		holder := readHolder(path)
		if inheritedFrom(name, holder) {
			f.Close()
			return &Lock{store: s, name: name, inherited: true}, nil
		}
		if holder.PID == os.Getpid() {
			f.Close()
			return nil, fmt.Errorf("task %q: %w", name, ErrLockedHere)
		}
		if !told && waiting != nil {
			told = true
			waiting(holder)
		}
		select {
		case <-ctx.Done():
			f.Close()
			return nil, fmt.Errorf("waiting for task %q: %w", name, ctx.Err())
		case <-time.After(lockPoll):
		}
	}
	// Who holds it, for whoever waits next. A reader that comes between the
	// lock and this write sees no holder, and says "another tspace".
	info := fmt.Sprintf("pid=%d\ncommand=%s\nsince=%s\n", os.Getpid(),
		strings.ReplaceAll(command, "\n", " "), time.Now().UTC().Format(time.RFC3339))
	if err := f.Truncate(0); err == nil {
		_, _ = f.WriteAt([]byte(info), 0)
	}
	return &Lock{store: s, name: name, f: f}, nil
}

// inheritedFrom reports whether $TSPACE_LOCKED hands this process the lock
// holder has: the process that ran it holds it and is waiting for it. A stale
// value, as in a shell started from a hook that is long gone, names a process
// that is not the holder.
func inheritedFrom(name string, holder LockHolder) bool {
	for _, v := range strings.Split(os.Getenv(EnvLocked), ",") {
		task, pid, ok := strings.Cut(v, ":")
		if ok && task == name && holder.PID != 0 && pid == strconv.Itoa(holder.PID) {
			return true
		}
	}
	return false
}

func readHolder(path string) LockHolder {
	var h LockHolder
	b, err := os.ReadFile(path)
	if err != nil {
		return h
	}
	for _, line := range strings.Split(string(b), "\n") {
		k, v, _ := strings.Cut(line, "=")
		switch k {
		case "pid":
			h.PID, _ = strconv.Atoi(v)
		case "command":
			h.Command = v
		case "since":
			h.Since, _ = time.Parse(time.RFC3339, v)
		}
	}
	return h
}

// Update applies fn to the task's latest record under its lock and saves the
// result, so changes made by others meanwhile are kept. A task deleted
// meanwhile stays deleted: Update returns ErrNotFound.
func (s *Store) Update(ctx context.Context, name string, fn func(*Task) error) (*Task, error) {
	l, err := s.Lock(ctx, name, "tspace update "+name, nil)
	if err != nil {
		return nil, err
	}
	defer l.Unlock()
	t, err := s.Load(name)
	if err != nil {
		return nil, err
	}
	if err := fn(t); err != nil {
		return nil, err
	}
	if err := s.Save(t); err != nil {
		return nil, err
	}
	return t, nil
}
