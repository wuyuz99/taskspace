package cli

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	tsexec "github.com/wuyuz99/taskspace/internal/exec"
	"github.com/wuyuz99/taskspace/internal/runtime"
	"github.com/wuyuz99/taskspace/internal/task"
)

// claudeAgentsJSON is `claude agents --json` listing sessions.
func claudeAgentsJSON(t *testing.T, sessions ...map[string]any) string {
	t.Helper()
	b, err := json.Marshal(sessions)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestConfigAgentStatus(t *testing.T) {
	h := newHarness(t)
	if err := h.exec(t, h.app.newConfigCmd(), "agent-status", "codex"); err == nil || !strings.Contains(err.Error(), "can: claude") {
		t.Fatalf("unknown agent: %v", err)
	}
	if err := h.exec(t, h.app.newConfigCmd(), "agent-status", "claude", "claude"); err != nil {
		t.Fatal(err)
	}
	c, err := h.app.Store.LoadConfig()
	if err != nil || len(c.AgentStatus) != 1 || c.AgentStatus[0] != "claude" {
		t.Fatalf("config = %+v, %v", c, err)
	}
	if err := h.exec(t, h.app.newConfigCmd(), "agent-status", "--unset"); err != nil {
		t.Fatal(err)
	}
	if c, _ := h.app.Store.LoadConfig(); len(c.AgentStatus) != 0 {
		t.Fatalf("after --unset: %+v", c.AgentStatus)
	}
}

// Turned on, Claude Code's own account of a session shows on the window in
// its directory, named as Claude's, and outranks a stale working signal.
func TestStatusReadsPublishedAgentStatus(t *testing.T) {
	h := newHarness(t)
	tk := h.startedRootTask(t, "PAY-142")
	repo := tk.RepoNames()[0]
	if err := h.app.Store.WriteSignal("PAY-142", repo, task.SignalWorking, ""); err != nil {
		t.Fatal(err)
	}
	h.app.Runtime = runningSession("PAY-142", map[string]runtime.PaneState{"_root": {}, repo: {}})
	h.app.run = tsexec.NewFake().RespondOut("claude agents --json", claudeAgentsJSON(t,
		map[string]any{"cwd": tk.Repos[repo].Worktree + string(filepath.Separator), "kind": "interactive", "status": "waiting", "waitingFor": "approve Bash"},
		map[string]any{"cwd": h.app.Store.TaskDir("PAY-142"), "kind": "interactive", "status": "busy"},
	))

	// Off until chosen.
	if err := h.exec(t, h.app.newStatusCmd()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(h.out.String(), "(claude)") {
		t.Fatalf("read without being turned on:\n%s", h.out.String())
	}

	if err := h.exec(t, h.app.newConfigCmd(), "agent-status", "claude"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newStatusCmd()); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(h.out.String()), "\n")
	if !strings.HasSuffix(lines[1], "working (claude)") || !strings.HasSuffix(lines[2], "blocked (claude)") {
		t.Fatalf("status:\n%s", h.out.String())
	}

	m := newDashModel(context.Background(), h.app, time.Second)
	m = drive(t, m, tea.WindowSizeMsg{Width: 200, Height: 40})
	m = drive(t, m, dashDataMsg(h.app.dashLoad(context.Background())))
	m = drive(t, m, key("j"))
	view := m.View()
	for _, want := range []string{"1 blocked", "blocked, as claude reports it now: approve Bash", "signal   working"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view lacks %q:\n%s", want, view)
		}
	}
}

// A setting left in the config keeps working, and says it is going (#140).
func TestAgentStatusIsDeprecated(t *testing.T) {
	h := newHarness(t)
	if err := h.exec(t, h.app.newConfigCmd(), "agent-status", "claude"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.out.String()+h.errb.String(), "deprecated") {
		t.Fatalf("config agent-status says nothing of going:\n%s%s", h.out.String(), h.errb.String())
	}
	h.out.Reset()
	h.errb.Reset()
	h.app.run = tsexec.NewFake().RespondOut("claude agents --json", "[]")
	if err := h.exec(t, h.app.newStatusCmd()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.errb.String(), "config agent-status is set; it is deprecated") {
		t.Fatalf("status warns:\n%s", h.errb.String())
	}
}
