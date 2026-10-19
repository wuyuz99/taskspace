package task

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ErrNotFound is returned when a task does not exist.
var ErrNotFound = errors.New("task not found")

// ErrExists is returned when creating a task that already exists.
var ErrExists = errors.New("task already exists")

// SkippedError reports entries a listing could not read.
//
// Listings return it alongside everything they *could* read, so a single
// corrupt or unreadable file degrades one row rather than making the whole
// command fail. Callers that want the rows should report it as a warning.
type SkippedError struct{ Errs []error }

func (e *SkippedError) Error() string {
	parts := make([]string, len(e.Errs))
	for i, err := range e.Errs {
		parts[i] = err.Error()
	}
	return "skipped unreadable entries: " + strings.Join(parts, "; ")
}

func (e *SkippedError) Unwrap() []error { return e.Errs }

// orNil returns a pointer to a copy only when something was actually skipped,
// so the happy path returns a genuinely nil error.
func (e SkippedError) orNil() error {
	if len(e.Errs) == 0 {
		return nil
	}
	return &e
}

// Store persists tasks as one JSON file per task.
//
// One file per task rather than one global file: the premise of the tool is
// many concurrent tasks, and a single file means two tspace processes racing on a
// read-modify-write of unrelated tasks. Writes are atomic via temp file +
// rename, so a reader never sees half a record; commands changing the same task
// serialize on its lock (see Lock), and Save is for a caller holding it that
// loaded the record under it, as Update does.
type Store struct {
	root string
}

// DefaultRoot resolves the tspace state root: $TSPACE_ROOT if set, else ~/.tspace.
//
// It is overridable because users already keep workspace directories in a
// chosen, visible location, and a hidden ~/.tspace should not be forced onto an
// existing habit.
func DefaultRoot() (string, error) {
	if v := strings.TrimSpace(os.Getenv(EnvRoot)); v != "" {
		abs, err := filepath.Abs(v)
		if err != nil {
			return "", fmt.Errorf("resolve TSPACE_ROOT: %w", err)
		}
		return abs, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home directory: %w", err)
	}
	return filepath.Join(home, ".tspace"), nil
}

// EnvRoot names the variable that chooses the state root.
const EnvRoot = "TSPACE_ROOT"

// NearestRoot is the closest directory at or above dir that holds tspace
// state, or "" if there is none. A root has both a tasks and a worktrees
// directory: a project with a tasks folder, or with a config.json beside it,
// is not one.
func NearestRoot(dir string) string {
	for {
		if isRoot(dir) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func isRoot(dir string) bool {
	s := NewStore(dir)
	for _, d := range []string{s.TasksDir(), s.WorktreesDir()} {
		if fi, err := os.Stat(d); err != nil || !fi.IsDir() {
			return false
		}
	}
	return true
}

// NewStore returns a store rooted at root.
func NewStore(root string) *Store { return &Store{root: root} }

// Open returns a store at the default root.
func Open() (*Store, error) {
	root, err := DefaultRoot()
	if err != nil {
		return nil, err
	}
	return NewStore(root), nil
}

// Root returns the state root.
func (s *Store) Root() string { return s.root }

// TasksDir is where task JSON files live.
func (s *Store) TasksDir() string { return filepath.Join(s.root, "tasks") }

// WorktreesDir is the parent of all task worktree directories.
func (s *Store) WorktreesDir() string { return filepath.Join(s.root, "worktrees") }

// TaskDir is the workspace directory for a task: the folder that contains one
// subdirectory per repo worktree.
func (s *Store) TaskDir(name string) string { return filepath.Join(s.WorktreesDir(), name) }

// WorktreePath is where a given repo's worktree for a task lives.
func (s *Store) WorktreePath(taskName, repoName string) string {
	return filepath.Join(s.TaskDir(taskName), repoName)
}

// ContextDir is reserved for future cross-repo task context. Reserving the
// location now means every worktree can reach it as ../.context/ later without
// a migration. Unused in v0.1.
func (s *Store) ContextDir(taskName string) string {
	return filepath.Join(s.TaskDir(taskName), ".context")
}

func (s *Store) path(name string) string {
	return filepath.Join(s.TasksDir(), name+".json")
}

// Init creates the state directories. Every write path also creates what it
// needs, so calling this is never required.
func (s *Store) Init() error {
	for _, d := range []string{s.TasksDir(), s.WorktreesDir()} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", d, err)
		}
	}
	return nil
}

// Load reads one task.
func (s *Store) Load(name string) (*Task, error) {
	if err := ValidateName("task", name); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(s.path(name))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%q: %w", name, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("read task %q: %w", name, err)
	}

	var t Task
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("parse task %q (%s): %w", name, s.path(name), err)
	}
	if t.Repos == nil {
		t.Repos = map[string]Repo{}
	}
	// Trust the filename over the recorded name; the file is the identity.
	t.Name = name
	if err := checkVersion(&t); err != nil {
		return nil, err
	}
	return &t, nil
}

// Exists reports whether a task record exists.
func (s *Store) Exists(name string) bool {
	_, err := os.Stat(s.path(name))
	return err == nil
}

// Save writes a task atomically: temp file in the same directory, then rename.
// It brings an outdated record to the current format first; see version.go.
func (s *Store) Save(t *Task) error {
	if err := ValidateName("task", t.Name); err != nil {
		return err
	}
	if err := s.upgrade(t); err != nil {
		return err
	}
	if err := os.MkdirAll(s.TasksDir(), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", s.TasksDir(), err)
	}

	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return fmt.Errorf("encode task %q: %w", t.Name, err)
	}
	data = append(data, '\n')

	final := s.path(t.Name)
	tmp, err := os.CreateTemp(s.TasksDir(), "."+t.Name+".*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file for task %q: %w", t.Name, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeds

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write task %q: %w", t.Name, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync task %q: %w", t.Name, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file for task %q: %w", t.Name, err)
	}
	// 0600, not 0644: a task record now carries the brief and handoff the user
	// wrote, and CreatePrepared already publishes at 0600. Matching it here
	// stops a record's mode from widening on its first update.
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return fmt.Errorf("chmod task %q: %w", t.Name, err)
	}
	if err := os.Rename(tmpName, final); err != nil {
		return fmt.Errorf("commit task %q: %w", t.Name, err)
	}
	return nil
}

// Create saves a new task, refusing to overwrite an existing one.
func (s *Store) Create(name string) (*Task, error) {
	if err := ValidateName("task", name); err != nil {
		return nil, err
	}
	// Published without replacing, as CreatePrepared does, so of two creates
	// at once exactly one wins, lock or no lock.
	t := New(name)
	if err := s.CreatePrepared(t); err != nil {
		return nil, err
	}
	return t, nil
}

// Delete removes a task record. It does not touch worktrees; that is tspace clean's
// job, and conflating the two is how a tool deletes something the user only
// wanted to tidy.
func (s *Store) Delete(name string) error {
	if err := ValidateName("task", name); err != nil {
		return err
	}
	err := os.Remove(s.path(name))
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%q: %w", name, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("delete task %q: %w", name, err)
	}
	return nil
}

// List returns every task, sorted by name.
func (s *Store) List() ([]*Task, error) {
	entries, err := os.ReadDir(s.TasksDir())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", s.TasksDir(), err)
	}

	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		// Skip temp files from an interrupted write.
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		names = append(names, strings.TrimSuffix(e.Name(), ".json"))
	}
	sort.Strings(names)

	tasks := make([]*Task, 0, len(names))
	var skipped SkippedError
	for _, n := range names {
		t, err := s.Load(n)
		if err != nil {
			skipped.Errs = append(skipped.Errs, err)
			continue
		}
		tasks = append(tasks, t)
	}
	return tasks, skipped.orNil()
}
