package task

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// The task's Markdown documents, inside its .context directory.
//
// They are files rather than fields in the task record because with no ticket
// system behind them they are the *original* of what a task is about, not a
// copy of something upstream. Files can be grepped, diffed, backed up, opened
// in an editor — and read by an agent working in one of the task's worktrees,
// which is the whole reason `.context/` was reserved next to them.
const (
	// BriefFile holds the requirements: what this task is for.
	BriefFile = "TASK.md"
	// HandoffFile holds local progress: where you left off.
	HandoffFile = "HANDOFF.md"
)

// Note is one of a task's Markdown documents.
//
// UpdatedAt is the file's modification time, so there is no stored timestamp
// that can disagree with the text it describes.
type Note struct {
	Text      string
	UpdatedAt time.Time
}

// NotePath is where a note lives. The task's worktrees are siblings of
// .context, so a program running in any of them reaches a note as
// ../.context/TASK.md.
func (s *Store) NotePath(taskName, file string) string {
	return filepath.Join(s.ContextDir(taskName), file)
}

// ReadNote reads one note. A note that was never written is empty, not an error.
func (s *Store) ReadNote(taskName, file string) (Note, error) {
	path := s.NotePath(taskName, file)
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Note{}, nil
	}
	if err != nil {
		return Note{}, fmt.Errorf("read note %s: %w", path, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return Note{}, fmt.Errorf("read note %s: %w", path, err)
	}
	return Note{Text: string(b), UpdatedAt: info.ModTime().UTC()}, nil
}

// WriteNote replaces a note, creating the context directory if needed.
//
// Empty text removes the file: a cleared note should leave nothing behind, not
// an empty file with a fresh modification time.
func (s *Store) WriteNote(taskName, file, text string) error {
	if err := ValidateName("task", taskName); err != nil {
		return err
	}
	path := s.NotePath(taskName, file)
	if text == "" {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("clear note %s: %w", path, err)
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	// Written through a temp file so an interrupted save cannot truncate a note
	// the user spent time on. 0600 for the same reason task records are.
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+file+".*.tmp")
	if err != nil {
		return fmt.Errorf("write note %s: %w", path, err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(text); err != nil {
		tmp.Close()
		return fmt.Errorf("write note %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write note %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("write note %s: %w", path, err)
	}
	return nil
}

// RemoveNotes deletes the task's context directory and reports what it held.
//
// Nothing else removes it: `clean` empties the task directory but leaves
// .context alone, so notes outlive the worktrees they describe.
func (s *Store) RemoveNotes(taskName string) ([]string, error) {
	if err := ValidateName("task", taskName); err != nil {
		return nil, err
	}
	dir := s.ContextDir(taskName)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}
	var held []string
	for _, e := range entries {
		held = append(held, e.Name())
	}
	if err := os.RemoveAll(dir); err != nil {
		return nil, fmt.Errorf("remove %s: %w", dir, err)
	}
	// The workspace directory goes too, but only if nothing else is in it.
	_ = os.Remove(s.TaskDir(taskName))
	return held, nil
}

// moveLegacyNotes is the migration to version 1: it writes t's inline notes to
// files and clears them from t, which is saved afterwards.
//
// A note file that already holds text wins: it is the newer of the two by
// definition. An empty file holds no note, so the inline text fills it. Run
// again after a failure part way, it finds the notes it already wrote and
// leaves them.
func (s *Store) moveLegacyNotes(t *Task) error {
	var handoffAt time.Time
	if t.LegacyHandoffUpdatedAt != nil {
		handoffAt = *t.LegacyHandoffUpdatedAt
	}
	moves := []struct {
		file, text string
		at         time.Time
	}{
		{BriefFile, t.LegacyBrief, time.Time{}},
		{HandoffFile, t.LegacyHandoff, handoffAt},
	}
	for _, m := range moves {
		if m.text == "" {
			continue
		}
		existing, err := s.ReadNote(t.Name, m.file)
		if err != nil {
			return err
		}
		if existing.Text != "" {
			continue
		}
		if err := s.WriteNote(t.Name, m.file, m.text); err != nil {
			return err
		}
		// Keep the handoff's recorded time; the file's own mtime replaces the
		// stored field from here on.
		if !m.at.IsZero() {
			_ = os.Chtimes(s.NotePath(t.Name, m.file), m.at, m.at)
		}
	}
	t.LegacyBrief, t.LegacyHandoff, t.LegacyHandoffUpdatedAt = "", "", nil
	return nil
}
