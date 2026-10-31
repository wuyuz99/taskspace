package task

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// What tspace remembers about a task's session between runs, beside the
// record rather than in it: it changes on every start and stop, and the
// record is configuration. None of it is the user's, so clean leaves it and
// Delete takes it with the record.

// RunDir holds a task's session memory.
func (s *Store) RunDir(taskName string) string { return filepath.Join(s.root, "run", taskName) }

func (s *Store) launchedPath(taskName, window string) string {
	return filepath.Join(s.RunDir(taskName), "launched", window)
}

// MarkLaunched records that window's agent has run, in the directory it
// runs in, so a later start may resume its conversation there.
func (s *Store) MarkLaunched(taskName, window string) error {
	p := s.launchedPath(taskName, window)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return fmt.Errorf("record launch: %w", err)
	}
	if err := os.WriteFile(p, nil, 0o644); err != nil {
		return fmt.Errorf("record launch: %w", err)
	}
	return nil
}

// Launched reports whether window's agent has run since its directory was
// made.
func (s *Store) Launched(taskName, window string) bool {
	_, err := os.Stat(s.launchedPath(taskName, window))
	return err == nil
}

// ForgetLaunched records that window's directory is new: whatever ran there
// before is gone with the old one.
func (s *Store) ForgetLaunched(taskName, window string) error {
	if err := os.Remove(s.launchedPath(taskName, window)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("forget launch: %w", err)
	}
	return nil
}

func (s *Store) openedWindowPath(taskName, name string) string {
	return filepath.Join(s.RunDir(taskName), "windows", strings.TrimPrefix(name, ExtraWindowPrefix))
}

// RememberWindow records what an unsaved extra window was opened with, so
// restart can relaunch it and `window --save` can keep it. Nothing but those
// reads it: it is not a saved window, so start does not open it and a recipe
// does not carry it.
func (s *Store) RememberWindow(taskName string, w Window) error {
	b, err := json.Marshal(w)
	if err != nil {
		return fmt.Errorf("record window: %w", err)
	}
	p := s.openedWindowPath(taskName, w.Name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return fmt.Errorf("record window: %w", err)
	}
	// A typed command can hold a secret; the record is private for the same reason.
	if err := os.WriteFile(p, b, 0o600); err != nil {
		return fmt.Errorf("record window: %w", err)
	}
	return nil
}

// OpenedWindow is what RememberWindow recorded for the window named name,
// with or without its prefix. The name may be anything typed, so one that no
// window could have is simply not known.
func (s *Store) OpenedWindow(taskName, name string) (Window, bool) {
	if ValidateName("window", strings.TrimPrefix(name, ExtraWindowPrefix)) != nil {
		return Window{}, false
	}
	b, err := os.ReadFile(s.openedWindowPath(taskName, name))
	if err != nil {
		return Window{}, false
	}
	var w Window
	if json.Unmarshal(b, &w) != nil {
		return Window{}, false
	}
	return w, true
}

// ForgetWindow drops what RememberWindow recorded for one window.
func (s *Store) ForgetWindow(taskName, name string) error {
	if err := os.Remove(s.openedWindowPath(taskName, name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("forget window: %w", err)
	}
	return nil
}

// ForgetWindows drops what RememberWindow recorded for every window, once the
// session that held them is gone.
func (s *Store) ForgetWindows(taskName string) error {
	if err := os.RemoveAll(filepath.Join(s.RunDir(taskName), "windows")); err != nil {
		return fmt.Errorf("forget windows: %w", err)
	}
	return nil
}

// RenameWindow carries what RememberWindow recorded to the window's new name.
// Whatever was recorded for that name belonged to some earlier window.
func (s *Store) RenameWindow(taskName, from, to string) error {
	w, ok := s.OpenedWindow(taskName, from)
	// Both go first: on a case-insensitive disk they can be one file.
	for _, name := range []string{from, to} {
		if err := s.ForgetWindow(taskName, name); err != nil {
			return err
		}
	}
	if !ok {
		return nil
	}
	w.Name = strings.TrimPrefix(to, ExtraWindowPrefix)
	return s.RememberWindow(taskName, w)
}

func (s *Store) runningPath(taskName string) string {
	return filepath.Join(s.RunDir(taskName), "running")
}

// Running is what start left running in a task's session, for bringing
// back just that once something else ends it.
type Running struct {
	// Whole is the whole task, as start without arguments starts it.
	Whole bool `json:"whole,omitempty"`
	// Root is the root window, and Repos these repositories' windows.
	Root  bool     `json:"root,omitempty"`
	Repos []string `json:"repos,omitempty"`
}

// With is what runs once r and o both do.
func (r Running) With(o Running) Running {
	out := Running{Whole: r.Whole || o.Whole, Root: r.Root || o.Root}
	for _, name := range append(slices.Clone(r.Repos), o.Repos...) {
		if !slices.Contains(out.Repos, name) {
			out.Repos = append(out.Repos, name)
		}
	}
	return out
}

// MarkRunning records that the task's session is up, running r.
func (s *Store) MarkRunning(taskName string, r Running) error {
	b, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("record start: %w", err)
	}
	if err := os.MkdirAll(s.RunDir(taskName), 0o755); err != nil {
		return fmt.Errorf("record start: %w", err)
	}
	if err := os.WriteFile(s.runningPath(taskName), b, 0o644); err != nil {
		return fmt.Errorf("record start: %w", err)
	}
	return nil
}

// Running reports what MarkRunning last recorded the task's session runs,
// and whether it recorded anything since the session last ended on purpose.
// A record from a tspace that kept none is the whole task.
func (s *Store) Running(taskName string) (Running, bool) {
	b, err := os.ReadFile(s.runningPath(taskName))
	if err != nil {
		return Running{}, false
	}
	var r Running
	if len(bytes.TrimSpace(b)) == 0 || json.Unmarshal(b, &r) != nil {
		return Running{Whole: true}, true
	}
	return r, true
}

// ClearRunning records that the task's session was ended on purpose: by
// stop, clean, or closing its last window.
func (s *Store) ClearRunning(taskName string) error {
	if err := os.Remove(s.runningPath(taskName)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("record stop: %w", err)
	}
	return nil
}

// Interrupted reports whether a task without a session lost it to something
// other than tspace ending it, such as a computer restart: start left it
// running and nothing since recorded an end. The caller knows there is no
// session. A task last run by an older tspace never counts.
func (s *Store) Interrupted(taskName string) bool {
	_, err := os.Stat(s.runningPath(taskName))
	return err == nil
}
