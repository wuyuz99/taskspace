package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wuyuz99/taskspace/internal/task"
)

func readWorkspace(t *testing.T, path string) map[string]json.RawMessage {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("%s: %v\n%s", path, err, b)
	}
	return doc
}

func folderPaths(t *testing.T, doc map[string]json.RawMessage) []string {
	t.Helper()
	var folders []struct{ Name, Path string }
	if err := json.Unmarshal(doc["folders"], &folders); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, f := range folders {
		out = append(out, f.Name+"="+f.Path)
	}
	return out
}

func TestEditorWorkspaceFile(t *testing.T) {
	h := newHarness(t)
	// A state directory with a space in its path.
	h.app.Store = task.NewStore(filepath.Join(t.TempDir(), "my state"))
	other := mkRepo(t)
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-1", "--repo", "backend="+h.repo, "--repo", "frontend="+other); err != nil {
		t.Fatal(err)
	}
	// Nothing to open before start.
	if err := h.exec(t, h.app.newEditorCmd(), "PAY-1", "--print"); err == nil || !strings.Contains(err.Error(), "tspace start PAY-1") {
		t.Fatalf("editor before start = %v", err)
	}
	if err := h.exec(t, h.app.newStartCmd(), "PAY-1", "backend", "--no-runtime"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newEditorCmd(), "PAY-1", "--print"); err != nil {
		t.Fatal(err)
	}
	path := strings.TrimSpace(h.out.String())
	if !strings.Contains(h.errb.String(), "left out frontend") {
		t.Errorf("stderr %q", h.errb.String())
	}
	if got := strings.Join(folderPaths(t, readWorkspace(t, path)), " "); got != "backend=../backend notes=." {
		t.Fatalf("folders %s", got)
	}
	// The editor finds the folders where the file says.
	if !isDir(filepath.Join(filepath.Dir(path), "..", "backend")) {
		t.Fatal("folder path does not lead to the worktree")
	}

	// What the user adds is kept; the folders follow the task.
	b, _ := os.ReadFile(path)
	b = []byte(strings.Replace(string(b), "{", `{"settings": {"editor.tabSize": 2},`, 1))
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newStartCmd(), "PAY-1", "--no-runtime"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newEditorCmd(), "PAY-1", "--print"); err != nil {
		t.Fatal(err)
	}
	doc := readWorkspace(t, path)
	if string(doc["settings"]) == "" || !strings.Contains(string(doc["settings"]), "tabSize") {
		t.Fatalf("settings lost: %s", doc["settings"])
	}
	if got := strings.Join(folderPaths(t, doc), " "); got != "backend=../backend frontend=../frontend notes=." {
		t.Fatalf("folders after start %s", got)
	}

	// A file with comments is left as it is.
	commented := "// mine\n{\"folders\": []}\n"
	if err := os.WriteFile(path, []byte(commented), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newEditorCmd(), "PAY-1", "--print"); err == nil || !strings.Contains(err.Error(), "leaves it alone") {
		t.Fatalf("editor over a commented file = %v", err)
	}
	if b, _ := os.ReadFile(path); string(b) != commented {
		t.Fatal("the commented file was rewritten")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	// The editor gets the file; a missing one says so, and where the file is.
	if err := h.exec(t, h.app.newConfigCmd(), "editor", "tspace-no-such-editor"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newEditorCmd(), "PAY-1"); err == nil || !strings.Contains(err.Error(), "not found") || !strings.Contains(err.Error(), "code-workspace") {
		t.Fatalf("missing editor = %v", err)
	}
	out := fakeShell(t)
	self, _ := os.Executable()
	if err := h.exec(t, h.app.newConfigCmd(), "editor", self, "--new-window"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newEditorCmd(), "PAY-1"); err != nil {
		t.Fatal(err)
	}
	if args := readShellEnv(t, out)["ARGS"]; !strings.HasPrefix(args, "--new-window ") || !strings.HasSuffix(args, "PAY-1.code-workspace") {
		t.Fatalf("editor ran with %q", args)
	}

	// task rm takes it with the notes.
	if err := h.exec(t, h.app.newCleanCmd(), "PAY-1", "--rm"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("workspace file left after clean --rm: %v", err)
	}
}
