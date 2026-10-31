package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tsgit "github.com/wuyuz99/taskspace/internal/git"
	"github.com/wuyuz99/taskspace/internal/task"
)

func TestCommandsFromRealPostCommitHook(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}
	h := newHarness(t)
	tk := h.startedTask(t, "hook-env")
	other := mkRepo(t)
	dir, err := os.MkdirTemp("", "tspace-hook-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	t.Setenv("TMUX_TMPDIR", dir)
	t.Setenv("TMUX", "")
	t.Cleanup(func() { exec.Command("tmux", "kill-server").Run() })
	// The agent exits after recording both the actual Git repository and env.
	tk.Agent = []string{"sh", "-c", `git rev-parse --show-toplevel > "$HOOK_PROBE/agent-repo"; env > "$HOOK_PROBE/agent-env"; touch "$HOOK_PROBE/done"`}
	if err := h.app.Store.Save(tk); err != nil {
		t.Fatal(err)
	}
	hook := `#!/bin/sh
set -eu
env > "$HOOK_PROBE/hook-env"
"$TSPACE_BIN" exec hook-env -- env > "$HOOK_PROBE/exec-env"
"$TSPACE_BIN" exec hook-env -- git rev-parse --show-toplevel > "$HOOK_PROBE/exec-repo"
"$TSPACE_BIN" start hook-env
`
	if err := os.WriteFile(filepath.Join(other, ".git", "hooks", "post-commit"), []byte(hook), 0755); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "-c", "user.name=tspace", "-c", "user.email=t@example.invalid", "commit", "--allow-empty", "-m", "exercise hook")
	cmd.Dir = other
	cmd.Env = append(tsgit.RepositoryEnv(cmd.Environ()), "TSPACE_TEST_CLI=1", "TSPACE_ROOT="+h.app.Store.Root(), "TSPACE_MULTIPLEXER=tmux", "TSPACE_BIN="+self, "HOOK_PROBE="+dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("commit: %v: %s", err, out)
	}
	read := func(name string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	for deadline := time.Now().Add(5 * time.Second); ; {
		if _, err := os.Stat(filepath.Join(dir, "done")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("agent never completed")
		}
		time.Sleep(20 * time.Millisecond)
	}
	hookEnv := "\n" + read("hook-env")
	hookKeys := []string{"GIT_EDITOR", "GIT_INDEX_FILE", "GIT_PREFIX", "GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_AUTHOR_DATE"}
	for _, key := range hookKeys {
		if !strings.Contains(hookEnv, "\n"+key+"=") {
			t.Fatalf("real commit hook did not supply %s", key)
		}
	}
	wt := tk.Repos[tk.RepoNames()[0]].Worktree
	for _, name := range []string{"exec-repo", "agent-repo"} {
		if got := read(name); !strings.Contains(got, wt+"\n") {
			t.Errorf("%s = %q, want %q", name, got, wt)
		}
	}
	for _, name := range []string{"exec-env", "agent-env"} {
		got := "\n" + read(name)
		for _, key := range tsgit.RepositoryEnvVars() {
			if strings.Contains(got, "\n"+key+"=") {
				t.Errorf("%s inherited %s", name, key)
			}
		}
	}
	for _, key := range hookKeys {
		if out, err := exec.Command("tmux", "show-environment", "-g", key).CombinedOutput(); err == nil {
			t.Errorf("server retained %s: %s", key, out)
		}
	}
}

func TestNoteEditorClearsRepositoryEnvironment(t *testing.T) {
	h := newHarness(t)
	if _, err := h.app.Store.Create("note-env"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_DIR", "/wrong/repo")
	t.Setenv("GIT_EDITOR", ":")
	t.Setenv("VISUAL", `env >`)
	if err := h.exec(t, h.app.newTaskCmd(), "brief", "note-env", "--edit"); err != nil {
		t.Fatal(err)
	}
	note, err := h.app.Store.ReadNote("note-env", task.BriefFile)
	if err != nil {
		t.Fatal(err)
	}
	if note.Text == "" {
		t.Fatal("editor did not record its environment")
	}
	if strings.Contains(note.Text, "GIT_DIR=") || strings.Contains(note.Text, "GIT_EDITOR=") {
		t.Fatal("editor inherited Git context")
	}
}
