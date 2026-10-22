package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/wuyuz99/taskspace/internal/task"
)

// notesFolder names the notes in the workspace. A repository name starts
// with a letter or digit, so none can take it.
const notesFolder = ".context"

func (a *App) newEditorCmd() *cobra.Command {
	var printOnly bool
	cmd := &cobra.Command{
		Use:   "editor [TASK]",
		Short: "Open a task's worktrees together in your editor",
		Long: "editor writes the task's worktrees, and its notes, into one .code-workspace\n" +
			"file, which VS Code and the editors built on it open as a multi-root\n" +
			"workspace, and opens it with the command from `tspace config editor`:\n\n" +
			"  tspace config editor code\n" +
			"  tspace editor PAY-142\n\n" +
			"The file is .context/TASK.code-workspace. clean removes it with the\n" +
			"worktrees it lists, and editor writes it again. Each run brings its folders\n" +
			"up to date with the task; folders you add from outside the task, settings,\n" +
			"extensions and anything else in the file are kept, and a file with nothing\n" +
			"to change is not touched. A worktree not created yet is left out until\n" +
			"start makes it. A file tspace cannot read as plain JSON, as with comments\n" +
			"in it, is left as it is.\n\n" +
			"It starts nothing and removes nothing; the editor's windows and terminals\n" +
			"are its own. --print writes the file and prints its path without opening\n" +
			"it. Run tspace where the editor runs: an editor in WSL, or one reaching a\n" +
			"remote machine, opens the file the paths in it are valid on.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// The file is written under the task's lock, as notes are, and the
			// editor opened after it is released.
			t, unlock, err := a.lockTask(cmd, taskArg(args), false)
			if err != nil {
				return err
			}
			path, err := a.writeCodeWorkspace(t)
			unlock()
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
			if err := a.launch(cmd.Context(), argv); err != nil {
				return fmt.Errorf("%w; the workspace is at %s", err, path)
			}
			a.printf("opened %s in %s\n", t.Name, argv[0])
			return nil
		},
	}
	cmd.Flags().BoolVar(&printOnly, "print", false, "write the workspace file and print its path, without opening it")
	return cmd
}

type workspaceFolder struct {
	Name string `json:"name,omitempty"`
	Path string `json:"path"`
}

// writeCodeWorkspace brings the task's .code-workspace file up to date and
// returns its path. The folders inside the task are tspace's, made from the
// task as it is now; folders elsewhere, and every other key, are the user's
// and kept as they were, in their place.
func (a *App) writeCodeWorkspace(t *task.Task) (string, error) {
	path := a.Store.CodeWorkspacePath(t.Name)
	dir := filepath.Dir(path)
	var folders []workspaceFolder
	var missing []string
	for _, name := range t.RepoNames() {
		wt := t.Repos[name].Worktree
		if !isDir(wt) {
			missing = append(missing, name)
			continue
		}
		rel, err := filepath.Rel(dir, wt)
		if err != nil {
			rel = wt
		}
		folders = append(folders, workspaceFolder{Name: name, Path: filepath.ToSlash(rel)})
	}
	if len(folders) == 0 {
		return "", fmt.Errorf("%s has no worktrees yet; create them with: tspace start %s", t.Name, t.Name)
	}
	if len(missing) > 0 {
		a.warnf("note: left out %s, whose worktree does not exist yet; tspace start %s makes it\n", joinNames(missing), t.Name)
	}
	folders = append(folders, workspaceFolder{Name: notesFolder, Path: "."})

	doc := &object{}
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
	if len(bytes.TrimSpace(b)) > 0 {
		if doc, err = parseObject(b); err != nil {
			return "", fmt.Errorf("%s is not plain JSON (%v), so tspace leaves it alone; remove it to have it written again", path, err)
		}
		// Folders the user added from outside the task stay, after tspace's.
		if raw, ok := doc.get("folders"); ok {
			var had []workspaceFolder
			if err := json.Unmarshal(raw, &had); err == nil {
				taskDir := a.Store.TaskDir(t.Name)
				for _, f := range had {
					p := filepath.FromSlash(f.Path)
					if !filepath.IsAbs(p) {
						p = filepath.Join(dir, p)
					}
					if !within(p, taskDir) {
						folders = append(folders, f)
					}
				}
			}
		}
	}
	raw, err := marshalPlain(folders)
	if err != nil {
		return "", err
	}
	if old, ok := doc.get("folders"); ok {
		if same, err := sameJSON(old, raw); err == nil && same {
			return path, nil // nothing to change, so nothing is rewritten
		}
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
