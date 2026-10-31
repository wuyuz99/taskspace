package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/wuyuz99/taskspace/internal/git"
	"github.com/wuyuz99/taskspace/internal/task"
)

// editTaskNote opens the note in the user's editor.
//
// The editor opens the note file itself, not a copy. There is nothing to
// reconcile afterwards and nothing to lose if the editor or the machine dies
// mid-edit: what the editor saved is the note. Two people editing the same note
// at once is the editor's problem to warn about, as it is for any other file.
func (a *App) editTaskNote(ctx context.Context, t *task.Task, noteFile string) error {
	path := a.Store.NotePath(t.Name, noteFile)
	before, err := a.Store.ReadNote(t.Name, noteFile)
	if err != nil {
		return err
	}
	// Create the file first: some editors are unhappy opening a path whose
	// directory does not exist, and an empty note should still be editable.
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			return fmt.Errorf("create %s: %w", path, err)
		}
	}

	editor := strings.TrimSpace(os.Getenv("VISUAL"))
	if editor == "" {
		editor = strings.TrimSpace(os.Getenv("EDITOR"))
	}
	if editor == "" {
		editor = "vi"
	}
	// The editor is a user-configured shell command (e.g. code --wait).
	// Pass the path separately, never interpolate it into shell code.
	cmd := exec.CommandContext(ctx, "sh", "-c", editor+` "$1"`, "tspace-editor", path)
	cmd.Env = git.RepositoryEnv(cmd.Environ())
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("editor failed: %w; the note is at %s", err, path)
	}

	after, err := a.Store.ReadNote(t.Name, noteFile)
	if err != nil {
		return err
	}
	kind := "brief"
	if noteFile == task.HandoffFile {
		kind = "handoff"
	}
	switch {
	case after.Text == before.Text:
		a.printf("%s unchanged for %s\n", kind, t.Name)
	case after.Text == "":
		// Emptying the note in the editor means the same as --clear: no file,
		// and so no modification time either.
		if err := a.Store.WriteNote(t.Name, noteFile, ""); err != nil {
			return err
		}
		a.printf("cleared %s for %s\n", kind, t.Name)
	default:
		a.printf("updated %s for %s (%s)\n", kind, t.Name, path)
	}
	return nil
}
