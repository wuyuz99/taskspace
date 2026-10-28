package task

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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

func (s *Store) stoppedPath(taskName string) string {
	return filepath.Join(s.RunDir(taskName), "stopped")
}

// MarkStopped records that the task's session was ended on purpose, by
// stop or clean, rather than by the machine going down.
func (s *Store) MarkStopped(taskName string) error {
	if err := os.MkdirAll(s.RunDir(taskName), 0o755); err != nil {
		return fmt.Errorf("record stop: %w", err)
	}
	if err := os.WriteFile(s.stoppedPath(taskName), nil, 0o644); err != nil {
		return fmt.Errorf("record stop: %w", err)
	}
	return nil
}

// ClearStopped records that the task runs again.
func (s *Store) ClearStopped(taskName string) error {
	if err := os.Remove(s.stoppedPath(taskName)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("clear stop: %w", err)
	}
	return nil
}

// Interrupted reports whether a task without a session lost it to something
// other than stop or clean, such as a computer restart: it has run, and was
// not stopped on purpose. The caller knows there is no session.
//
// A task last run by a tspace that recorded no launches still counts if its
// agents left signals behind, since stop clears them and a crash does not.
func (s *Store) Interrupted(taskName string) bool {
	if _, err := os.Stat(s.stoppedPath(taskName)); err == nil {
		return false
	}
	for _, dir := range []string{filepath.Join(s.RunDir(taskName), "launched"), s.SignalsDir(taskName)} {
		if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
			return true
		}
	}
	return false
}
