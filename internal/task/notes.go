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

// CodeWorkspacePath is where `tspace editor` writes the task's
// .code-workspace file, beside its notes.
func (s *Store) CodeWorkspacePath(taskName string) string {
	return filepath.Join(s.ContextDir(taskName), taskName+".code-workspace")
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
	// Atomic so an interrupted save cannot truncate a note the user spent time
	// on. 0600 for the same reason task records are.
	if err := WriteFileAtomic(path, []byte(text), 0o600); err != nil {
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

// Notes reads a task's brief and handoff.
func (s *Store) Notes(t *Task) (brief, handoff Note, err error) {
	if brief, err = s.ReadNote(t.Name, BriefFile); err != nil {
		return brief, handoff, err
	}
	handoff, err = s.ReadNote(t.Name, HandoffFile)
	return brief, handoff, err
}
