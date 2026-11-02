package agents

import (
	"context"
	"encoding/json"
	"testing"

	tsexec "github.com/wuyuz99/taskspace/internal/exec"
	"github.com/wuyuz99/taskspace/internal/fspath"
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
	got, err := ClaudeAgents{Run: f}.Published(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if p := got[fspath.Key(a)]; p.State != status.ReportState(task.SignalBlocked) || p.Message != "approve Bash" || p.Source != "claude" {
		t.Errorf("a = %+v, want the waiting session", p)
	}
	if p := got[fspath.Key(b)]; p.State != status.ReportIdle {
		t.Errorf("b = %+v", p)
	}
	if _, ok := got[fspath.Key(c)]; ok || len(got) != 2 {
		t.Errorf("published = %+v", got)
	}

	f = tsexec.NewFake().RespondOut("claude agents --json", "not json")
	if _, err := (ClaudeAgents{Run: f}).Published(context.Background()); err == nil {
		t.Error("unreadable output accepted")
	}
}
