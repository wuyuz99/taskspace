package task

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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
//     other. Reading needs no lock, and never waits: records are replaced
//     atomically.
//   - Between processes the lock is an advisory OS lock (flock, or LockFileEx
//     on Windows) on tasks/.locks/TASK.lock. The OS drops it when its process
//     exits however it exits, so there is never a stale lock to clean up, and
//     lock files are never deleted: removing one while another process waits
//     on it would let two processes hold "the" lock. Within a process, as in
//     dash, commands on one task wait for each other the same way.
//   - A waiting command says who holds the lock, again whenever that changes,
//     and waits until it is free or its context is cancelled (Ctrl-C).
//   - The lock travels in the context (WithLock): code that already holds it,
//     such as `task create --start` running start, takes it again for free.
//     Code that holds a lock must pass that context on; taking the lock
//     without it waits for itself until the context is cancelled.
//   - A process the holder runs and waits for, such as a post_create hook, gets
//     the lock in $TSPACE_LOCKED and may change the record, which the holder
//     reloads before using it again. It may not run commands that take the
//     lock to change worktrees or the session (Inherited reports that): the
//     holder is partway through doing that itself. The variable names this
//     one acquisition, so a copy that outlives it, as in a background job
//     the hook left behind, does not match the next.
//   - Nobody holds a lock while attached to a terminal: open releases it
//     before it attaches.
type Lock struct {
	store *Store
	name  string
	// f is the locked file and token this acquisition's name for itself; both
	// unset for a lock this one re-enters or inherits.
	f         *os.File
	token     string
	inherited bool
	release   func()
	once      sync.Once
	// outer is the lock this one re-enters, which holds the file and token.
	outer *Lock
	// released is set by Unlock; a released lock no longer re-enters.
	released atomic.Bool
}

// held is the acquisition l stands for.
func (l *Lock) held() *Lock {
	for l.outer != nil {
		l = l.outer
	}
	return l
}

// EnvLocked passes the task locks held by the processes a process was run by,
// and waits on, to it: TASK:TOKEN entries separated by commas.
const EnvLocked = "TSPACE_LOCKED"

// lockPoll is how often a waiting command tries again.
const lockPoll = 50 * time.Millisecond

type lockKey struct{ root, name string }

// WithLock returns ctx carrying l, so code given ctx re-enters l.
func WithLock(ctx context.Context, l *Lock) context.Context {
	return context.WithValue(ctx, lockKey{l.store.root, l.name}, l)
}

// HeldLock is the lock on name that ctx carries, or nil, also once that lock
// has been released, as by open before it attaches.
func (s *Store) HeldLock(ctx context.Context, name string) *Lock {
	l, _ := ctx.Value(lockKey{s.root, name}).(*Lock)
	if l == nil || l.held().released.Load() {
		return nil
	}
	return l
}

// Inherited reports whether the lock is held by the process that ran this
// one, such as the start running a post_create hook.
func (l *Lock) Inherited() bool { return l.held().inherited }

// Reentered reports whether l re-enters a lock its caller already holds.
func (l *Lock) Reentered() bool { return l.outer != nil }

// Env is the variable that hands the lock to a process its holder waits for,
// along with the locks this process inherited: they are all held by processes
// waiting on that one.
func (l *Lock) Env() string {
	held := os.Getenv(EnvLocked)
	token := l.held().token
	if token == "" {
		return EnvLocked + "=" + held
	}
	mine := l.name + ":" + token
	if held != "" {
		mine = held + "," + mine
	}
	return EnvLocked + "=" + mine
}

// Unlock releases the lock. It is safe to call more than once; for a lock
// re-entered or inherited it does nothing.
func (l *Lock) Unlock() {
	l.once.Do(func() {
		if l.outer != nil {
			return
		}
		l.released.Store(true)
		if l.f != nil {
			// No holder is named while nobody holds it.
			_ = l.f.Truncate(0)
			_ = unlockFile(l.f)
			_ = l.f.Close()
		}
		if l.release != nil {
			l.release()
		}
	})
}

// LockPath is the file a task's lock is held on.
func (s *Store) LockPath(name string) string {
	return filepath.Join(s.TasksDir(), ".locks", name+".lock")
}

// LockHolder describes the holder of a lock, as far as it said.
type LockHolder struct {
	PID     int
	Command string
	Since   time.Time
	token   string
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

// inProcess holds, per lock file, a one-slot channel taken by whichever of
// this process's commands has that task, and that command's details. An OS
// lock does not order commands of one process: dash runs several at once.
var inProcess sync.Map // path -> *localLock

type localLock struct {
	slot   chan struct{}
	mu     sync.Mutex
	holder LockHolder
}

// Lock takes the task's lock for command, which is recorded for anyone who
// has to wait. If the lock is busy, waiting is called with its holder, and
// again if that changes, and Lock tries again until it is free or ctx is done.
// A ctx that is already done makes one attempt, for callers that must not
// wait.
func (s *Store) Lock(ctx context.Context, name, command string, waiting func(LockHolder)) (*Lock, error) {
	if err := ValidateName("task", name); err != nil {
		return nil, err
	}
	if held := s.HeldLock(ctx, name); held != nil {
		return &Lock{store: s, name: name, outer: held}, nil
	}
	path := s.LockPath(name)
	var told LockHolder
	toldAny := false
	tell := func(h LockHolder) {
		if waiting != nil && (!toldAny || h.PID != 0 && h != told) {
			told, toldAny = h, true
			waiting(h)
		}
	}
	wait := func() error {
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for task %q: %w", name, ctx.Err())
		case <-time.After(lockPoll):
			return nil
		}
	}

	// This process's other commands on the task first.
	v, ok := inProcess.Load(path)
	if !ok {
		v, _ = inProcess.LoadOrStore(path, &localLock{slot: make(chan struct{}, 1)})
	}
	local := v.(*localLock)
	for acquired := false; !acquired; {
		select {
		case local.slot <- struct{}{}:
			acquired = true
		default:
			local.mu.Lock()
			h := local.holder
			local.mu.Unlock()
			tell(h)
			if err := wait(); err != nil {
				return nil, err
			}
		}
	}
	release := func() {
		local.mu.Lock()
		local.holder = LockHolder{}
		local.mu.Unlock()
		<-local.slot
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		release()
		return nil, fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		release()
		return nil, fmt.Errorf("open lock for task %q: %w", name, err)
	}
	fail := func(err error) (*Lock, error) {
		f.Close()
		release()
		return nil, err
	}
	for {
		ok, err := tryLockFile(f)
		if err != nil {
			return fail(fmt.Errorf("lock task %q: %w", name, err))
		}
		if ok {
			break
		}
		holder := readHolder(path)
		if inheritedFrom(name, holder) {
			f.Close()
			release()
			return &Lock{store: s, name: name, inherited: true}, nil
		}
		tell(holder)
		if err := wait(); err != nil {
			return fail(err)
		}
	}

	token := newToken()
	holder := LockHolder{PID: os.Getpid(), Command: strings.ReplaceAll(command, "\n", " "),
		Since: time.Now().UTC().Truncate(time.Second), token: token}
	local.mu.Lock()
	local.holder = holder
	local.mu.Unlock()
	// For whoever waits next. One that reads before this sees no holder, and
	// says "another tspace" until it does.
	info := fmt.Sprintf("pid=%d\ncommand=%s\nsince=%s\ntoken=%s\n", holder.PID, holder.Command,
		holder.Since.Format(time.RFC3339), token)
	if err := f.Truncate(0); err == nil {
		_, _ = f.WriteAt([]byte(info), 0)
	}
	return &Lock{store: s, name: name, f: f, token: token, release: release}, nil
}

func newToken() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// inheritedFrom reports whether $TSPACE_LOCKED hands this process the lock
// holder has: the process that ran it holds it, in this very acquisition, and
// is waiting for it.
func inheritedFrom(name string, holder LockHolder) bool {
	if holder.token == "" {
		return false
	}
	for _, v := range strings.Split(os.Getenv(EnvLocked), ",") {
		task, token, ok := strings.Cut(v, ":")
		if ok && task == name && token == holder.token {
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
		case "token":
			h.token = v
		}
	}
	return h
}

// Update applies fn to the task's latest record under its lock and saves the
// result, so changes made by others meanwhile are kept. A task deleted
// meanwhile stays deleted: Update returns ErrNotFound. command names it for
// anyone who has to wait.
func (s *Store) Update(ctx context.Context, name, command string, fn func(*Task) error) (*Task, error) {
	l, err := s.Lock(ctx, name, command, nil)
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
