package runtime

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	tsexec "github.com/wuyuz99/taskspace/internal/exec"
)

func TestRealTmuxCloseOneWindow(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}
	dir, err := os.MkdirTemp("", "tspace-close-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	runner := isolatedRunner{filepath.Join(dir, "socket")}
	t.Cleanup(func() { runner.Run(ctx(), "", "tmux", "kill-server") })
	tm := NewTmux(runner)
	tm.StopGrace = 0
	session := tm.SessionName("close")
	if err := tm.Start(t.Context(), session, []Window{{Name: "one", Command: []string{"sleep", "60"}}, {Name: "two", Command: []string{"sleep", "60"}}}); err != nil {
		t.Fatal(err)
	}
	if err := tm.CloseWindow(t.Context(), session, "one"); err != nil {
		t.Fatal(err)
	}
	st, err := tm.State(t.Context(), session)
	if err != nil || !st.Exists || len(st.Panes) != 1 {
		t.Fatalf("state %+v err %v", st, err)
	}
	if _, ok := st.Panes["two"]; !ok {
		t.Fatal("closed the other window")
	}
	if err := tm.CloseWindow(t.Context(), session, "two"); err != nil {
		t.Fatal(err)
	}
	if exists, err := tm.HasSession(t.Context(), session); err != nil || exists {
		t.Fatalf("last window: exists=%v err=%v", exists, err)
	}
}

func TestZellijCloseTargetsOnlyRequestedTab(t *testing.T) {
	f := tsexec.NewFake().RespondOut("list-panes", zellijPanesJSON)
	z := zellijWith(f)
	if err := z.CloseWindow(t.Context(), "session", "backend"); err != nil {
		t.Fatal(err)
	}
	if !f.Ran("close-tab-by-id 1") || f.Ran("send-keys") || f.Ran("kill-session") {
		t.Fatalf("commands %v", f.Commands())
	}
}

func TestAdapterWithoutCloseDoesNotStopSession(t *testing.T) {
	f := tsexec.NewFake().Fail(" close ", "unsupported operation", 2)
	a := NewAdapter("custom", []string{"custom-adapter"}, f)
	if err := a.CloseWindow(ctx(), "session", "backend"); err == nil {
		t.Fatal("unsupported close succeeded")
	}
	if f.Ran(" stop ") {
		t.Fatal("fell back to stopping entire session")
	}
}
