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
