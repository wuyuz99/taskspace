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
	"github.com/wuyuz99/taskspace/internal/status"
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

func TestClaudeAgentsPublished(t *testing.T) {
	a, b, c := t.TempDir(), t.TempDir(), t.TempDir()
	out := claudeAgentsJSON(t,
		map[string]any{"pid": 1, "cwd": a, "kind": "interactive", "status": "busy"},
		map[string]any{"pid": 2, "cwd": a, "kind": "interactive", "status": "waiting", "waitingFor": "approve Bash"},
		map[string]any{"pid": 3, "cwd": b, "kind": "interactive", "status": "idle"},
		// A background session is not in a tspace window.
		map[string]any{"id": "x", "cwd": c, "kind": "background", "status": "busy"},
		// Nor is a finished one, which has a state and no status.
		map[string]any{"id": "y", "cwd": c, "kind": "interactive", "state": "failed"},
	)
	f := tsexec.NewFake().RespondOut("claude agents --json", out)
	got, err := claudeAgents{run: f}.Published(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if p := got[status.DirKey(a)]; p.State != status.ReportState(task.SignalBlocked) || p.Message != "approve Bash" || p.Source != "claude" {
		t.Errorf("a = %+v, want the waiting session", p)
	}
	if p := got[status.DirKey(b)]; p.State != status.ReportIdle {
		t.Errorf("b = %+v", p)
	}
	if _, ok := got[status.DirKey(c)]; ok || len(got) != 2 {
		t.Errorf("published = %+v", got)
	}

	f = tsexec.NewFake().RespondOut("claude agents --json", "not json")
	if _, err := (claudeAgents{run: f}).Published(context.Background()); err == nil {
		t.Error("unreadable output accepted")
	}
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
	h.app.Runtime = &windowRuntime{running: true, panes: map[string]runtime.PaneState{"_root": {}, repo: {}}}
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
