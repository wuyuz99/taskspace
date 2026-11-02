package agents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClaudeProject(t *testing.T) {
	for dir, want := range map[string]string{
		`C:\Users\Isaac\projects\workspace-management`: "C--Users-Isaac-projects-workspace-management",
		"/home/me/.tspace/tasks/PAY_142.x":             "-home-me--tspace-tasks-PAY-142-x",
		"/" + strings.Repeat("a", 199):                 "-" + strings.Repeat("a", 199),
		"/" + strings.Repeat("a", 200):                 "",
	} {
		if got := ClaudeProject(dir); got != want {
			t.Errorf("ClaudeProject(%q) = %q; want %q", dir, got, want)
		}
	}
}

func TestClaudeConversationsCarry(t *testing.T) {
	config := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	projects := filepath.Join(config, "projects")
	from, to := "/tasks/OLD/backend", "/tasks/NEW/backend"
	src, dst := filepath.Join(projects, "-tasks-OLD-backend"), filepath.Join(projects, "-tasks-NEW-backend")
	c := ClaudeConversations{}

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
