package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wuyuz99/taskspace/internal/task"
)

func TestClaudeProject(t *testing.T) {
	for dir, want := range map[string]string{
		`C:\Users\Isaac\projects\workspace-management`: "C--Users-Isaac-projects-workspace-management",
		"/home/me/.tspace/tasks/PAY_142.x":             "-home-me--tspace-tasks-PAY-142-x",
		"/" + strings.Repeat("a", 199):                 "-" + strings.Repeat("a", 199),
		"/" + strings.Repeat("a", 200):                 "",
	} {
		if got := claudeProject(dir); got != want {
			t.Errorf("claudeProject(%q) = %q; want %q", dir, got, want)
		}
	}
}

func TestClaudeConversationsCarry(t *testing.T) {
	config := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	projects := filepath.Join(config, "projects")
	from, to := "/tasks/OLD/backend", "/tasks/NEW/backend"
	src, dst := filepath.Join(projects, "-tasks-OLD-backend"), filepath.Join(projects, "-tasks-NEW-backend")
	c := claudeConversations{}

	if ok, err := c.Carry(from, to); ok || err != nil {
		t.Fatalf("no projects directory: %v, %v", ok, err)
	}
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "s.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if ok, err := c.Carry(from, to); !ok || err != nil {
		t.Fatalf("carry: %v, %v", ok, err)
	}
	if _, err := os.Stat(filepath.Join(dst, "s.jsonl")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatalf("old folder remains: %v", err)
	}

	// The new directory's own history is never overwritten.
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if ok, err := c.Carry(from, to); ok || err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("into an existing folder: %v, %v", ok, err)
	}
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("refused carry moved the folder: %v", err)
	}
	if ok, err := c.Carry(from, "/"+strings.Repeat("n", 300)); ok || err == nil {
		t.Fatalf("to an unnameable path: %v, %v", ok, err)
	}
}

func TestConfigCarryConversations(t *testing.T) {
	h := newHarness(t)
	if err := h.exec(t, h.app.newConfigCmd(), "carry-conversations", "codex"); err == nil || !strings.Contains(err.Error(), "can: claude") {
		t.Fatalf("unknown agent: %v", err)
	}
	if err := h.exec(t, h.app.newConfigCmd(), "carry-conversations", "claude", "--unset"); err == nil {
		t.Fatal("agents with --unset accepted")
	}
	if err := h.exec(t, h.app.newConfigCmd(), "carry-conversations", "claude", "claude"); err != nil {
		t.Fatal(err)
	}
	c, err := h.app.Store.LoadConfig()
	if err != nil || len(c.CarryConversations) != 1 || c.CarryConversations[0] != "claude" {
		t.Fatalf("config = %+v, %v", c, err)
	}
	h.out.Reset()
	if err := h.exec(t, h.app.newConfigCmd()); err != nil || !strings.Contains(h.out.String(), "conversations carried: claude") {
		t.Fatalf("config shows: %v\n%s", err, h.out.String())
	}
	if err := h.exec(t, h.app.newConfigCmd(), "carry-conversations", "--unset"); err != nil {
		t.Fatal(err)
	}
	if c, _ := h.app.Store.LoadConfig(); len(c.CarryConversations) != 0 {
		t.Fatalf("after --unset: %+v", c.CarryConversations)
	}
}

// Chosen, task rename takes Claude Code's history for the task root along
// and says so in place of the note.
func TestTaskRenameCarriesClaudeConversation(t *testing.T) {
	h := newHarness(t)
	claude := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claude)
	tk := h.startedRootTask(t, "OLD")
	tk.Agent, tk.Resume = []string{"claude"}, []string{"claude", "--continue"}
	if err := h.app.Store.Save(tk); err != nil {
		t.Fatal(err)
	}
	if err := h.app.Store.MarkLaunched("OLD", task.RootWindowName); err != nil {
		t.Fatal(err)
	}
	oldRoot := h.app.Store.TaskDir("OLD")
	src := filepath.Join(claude, "projects", claudeProject(oldRoot))
	if claudeProject(oldRoot) == "" {
		t.Skip("temporary directory too long for Claude Code's folder names")
	}
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newConfigCmd(), "carry-conversations", "claude"); err != nil {
		t.Fatal(err)
	}
	h.out.Reset()
	h.errb.Reset()
	if err := h.exec(t, h.app.newTaskCmd(), "rename", "OLD", "NEW"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(claude, "projects", claudeProject(h.app.Store.TaskDir("NEW")))); err != nil {
		t.Fatalf("history not carried: %v", err)
	}
	if !strings.Contains(h.out.String(), "(root)       carried its claude conversation from "+oldRoot) {
		t.Fatalf("output:\n%s", h.out.String())
	}
	if strings.Contains(h.errb.String(), "resumes by directory") {
		t.Fatalf("note for a carried conversation:\n%s", h.errb.String())
	}
}
