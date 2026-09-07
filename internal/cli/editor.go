package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/aw-tool/aw/internal/task"
)

func (a *App) editTaskNote(ctx context.Context, original *task.Task, handoff bool) error {
	kind, before := "brief", original.Brief
	if handoff {
		kind, before = "handoff", original.Handoff
	}
	f, err := os.CreateTemp("", "aw-"+original.Name+"-"+kind+"-*.md")
	if err != nil {
		return err
	}
	path := f.Name()
	if _, err := f.WriteString(before); err != nil {
		f.Close()
		os.Remove(path)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return err
	}
	// Failed edits remain recoverable, including editor and storage failures.
	retain := true
	defer func() {
		if !retain {
			os.Remove(path)
		}
	}()
	failure := func(err error) error { return fmt.Errorf("%w; draft kept at %s", err, path) }
	editor := strings.TrimSpace(os.Getenv("VISUAL"))
	if editor == "" {
		editor = strings.TrimSpace(os.Getenv("EDITOR"))
	}
	if editor == "" {
		editor = "vi"
	}
	// The editor is a user-configured shell command (e.g. code --wait).
	// Pass the generated file separately, never interpolate it into shell code.
	cmd := exec.CommandContext(ctx, "sh", "-c", editor+` "$1"`, "aw-editor", path)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return failure(fmt.Errorf("editor failed: %w", err))
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return failure(err)
	}
	note := string(b)
	if note == before {
		retain = false
		a.printf("%s unchanged for %s\n", kind, original.Name)
		return nil
	}
	// Reload after the potentially long editor session to preserve other metadata.
	latest, err := a.loadTask(original.Name)
	if err != nil {
		return failure(err)
	}
	current := latest.Brief
	if handoff {
		current = latest.Handoff
	}
	if current != before || (handoff && !latest.HandoffUpdatedAt.Equal(original.HandoffUpdatedAt)) {
		return failure(fmt.Errorf("%s changed while the editor was open; reconcile your draft with the current note", kind))
	}
	if handoff {
		latest.Handoff = note
		latest.HandoffUpdatedAt = handoffStamp(note)
	} else {
		latest.Brief = note
	}
	if err := a.Store.Save(latest); err != nil {
		return failure(err)
	}
	retain = false
	a.printf("updated %s for %s\n", kind, latest.Name)
	return nil
}
