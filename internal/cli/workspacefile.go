package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	osexec "os/exec"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/wuyuz99/taskspace/internal/task"
)

func (a *App) newEditorCmd() *cobra.Command {
	var printOnly bool
	cmd := &cobra.Command{
		Use:   "editor TASK",
		Short: "Open a task's worktrees together in your editor",
		Long: "editor writes the task's worktrees, and its notes, into one .code-workspace\n" +
			"file, which VS Code and the editors built on it open as a multi-root\n" +
			"workspace, and opens it with the command from `tspace config editor`:\n\n" +
			"  tspace config editor code\n" +
			"  tspace editor PAY-142\n\n" +
			"The file is .context/TASK.code-workspace, beside the notes, so clean keeps\n" +
			"it and task rm removes it. Each run rewrites only its folders, from the\n" +
			"task as it is now: settings, extensions and anything else you add to the\n" +
			"file are kept. A worktree not created yet is left out until start makes\n" +
			"it. A file tspace cannot read as plain JSON, as with comments in it, is\n" +
			"left as it is.\n\n" +
			"It starts nothing and removes nothing; the editor's windows and terminals\n" +
			"are its own. --print writes the file and prints its path without opening\n" +
			"it. Run tspace where the editor runs: an editor in WSL, or one reaching a\n" +
			"remote machine, opens the file the paths in it are valid on.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			t, err := a.loadTask(args[0])
			if err != nil {
				return err
			}
			path, err := a.writeCodeWorkspace(t)
			if err != nil {
				return err
			}
			c, err := a.Store.LoadConfig()
			if err != nil {
				return err
			}
			if printOnly || len(c.Editor) == 0 {
				a.printf("%s\n", path)
				if !printOnly {
					a.warnf("note: set an editor to open it with, e.g.: tspace config editor code\n")
				}
				return nil
			}
			argv := append(append([]string(nil), c.Editor...), path)
			if _, err := osexec.LookPath(argv[0]); err != nil {
				return fmt.Errorf("editor %s not found (%v); the workspace is at %s; change the editor with tspace config editor", argv[0], err, path)
			}
			ed := osexec.CommandContext(cmd.Context(), argv[0], argv[1:]...)
			ed.Stdout, ed.Stderr = a.Out, a.Err
			if err := ed.Run(); err != nil {
				return fmt.Errorf("%s: %w; the workspace is at %s", argv[0], err, path)
			}
			a.printf("opened %s in %s\n", t.Name, argv[0])
			return nil
		},
	}
	cmd.Flags().BoolVar(&printOnly, "print", false, "write the workspace file and print its path, without opening it")
	return cmd
}

// codeWorkspacePath is where a task's .code-workspace file lives.
func (a *App) codeWorkspacePath(t *task.Task) string {
	return filepath.Join(a.Store.ContextDir(t.Name), t.Name+".code-workspace")
}

// writeCodeWorkspace brings the task's .code-workspace file up to date and
// returns its path. Only "folders" is tspace's; every other key is kept as it
// was, in its place.
func (a *App) writeCodeWorkspace(t *task.Task) (string, error) {
	path := a.codeWorkspacePath(t)
	type folder struct {
		Name string `json:"name"`
		Path string `json:"path"`
	}
	var folders []folder
	var missing []string
	for _, name := range t.RepoNames() {
		wt := t.Repos[name].Worktree
		if !isDir(wt) {
			missing = append(missing, name)
			continue
		}
		rel, err := filepath.Rel(filepath.Dir(path), wt)
		if err != nil {
			rel = wt
		}
		folders = append(folders, folder{Name: name, Path: filepath.ToSlash(rel)})
	}
	if len(folders) == 0 {
		return "", fmt.Errorf("%s has no worktrees yet; create them with: tspace start %s", t.Name, t.Name)
	}
	if len(missing) > 0 {
		a.warnf("note: left out %s, whose worktree does not exist yet; tspace start %s makes it\n", joinNames(missing), t.Name)
	}
	// The notes, where an agent's brief and handoff are.
	folders = append(folders, folder{Name: "notes", Path: "."})

	doc := &object{}
	if b, err := os.ReadFile(path); err == nil {
		if doc, err = parseObject(b); err != nil {
			return "", fmt.Errorf("%s is not plain JSON (%v), so tspace leaves it alone; remove it to have it written again", path, err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	raw, err := marshalPlain(folders)
	if err != nil {
		return "", err
	}
	doc.set("folders", raw)
	var out bytes.Buffer
	if err := json.Indent(&out, doc.marshal(), "", "\t"); err != nil {
		return "", err
	}
	out.WriteByte('\n')
	if err := writeFileAtomic(path, out.Bytes()); err != nil {
		return "", err
	}
	return path, nil
}
