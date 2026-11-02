package runtime

import (
	"testing"

	tsexec "github.com/wuyuz99/taskspace/internal/exec"
)

func TestTmuxStatesKeepsOnlyTspaceSessions(t *testing.T) {
	f := tsexec.NewFake()
	f.RespondOut("list-panes -a", "tspace@a\t_root\t0\tclaude\t0\t100\n"+
		"mine\tvim\t0\tvim\t0\t100\n"+
		"tspace@a\tapi\t1\tsh\t1\t200\n"+
		"tspace@b\t_root\t0\tzsh\t0\t\n")
	got, err := NewTmux(f).States(ctx())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("sessions: %+v", got)
	}
	a := got["tspace@a"].State
	if !a.Exists || len(a.Panes) != 2 {
		t.Fatalf("tspace@a: %+v", a)
	}
	if p, ok := a.Pane("api"); !ok || !p.Dead || !p.Bell {
		t.Fatalf("api: %+v", p)
	}
}

func TestTmuxStatesWithNoServerIsNoSessions(t *testing.T) {
	f := tsexec.NewFake()
	f.Fail("list-panes -a", "no server running on /tmp/tmux-1000/default", 1)
	got, err := NewTmux(f).States(ctx())
	if err != nil || len(got) != 0 {
		t.Fatalf("States = %+v, %v", got, err)
	}
}

func TestTmuxStatesReportsOtherFailures(t *testing.T) {
	f := tsexec.NewFake()
	f.Fail("list-panes -a", "protocol version mismatch", 1)
	if _, err := NewTmux(f).States(ctx()); err == nil {
		t.Fatal("a server tmux cannot talk to reads as no sessions")
	}
}

func TestZellijStatesAsksOnlyLiveSessions(t *testing.T) {
	f := tsexec.NewFake()
	f.RespondOut("list-sessions", "tspace@up [Created 3m ago]\n"+
		"tspace@old [Created 2h ago] (EXITED - attach to resurrect)\n"+
		"mine [Created 1m ago]\n")
	f.RespondOut("--session tspace@up action list-panes", `[{"id":1,"tab_name":"_root","exited":false}]`)
	got, err := NewZellij(f).States(ctx())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got["tspace@up"].State.Exists {
		t.Fatalf("sessions: %+v", got)
	}
	for _, c := range f.Commands() {
		if c == "zellij --session tspace@old action list-panes --all --json" {
			t.Fatal("asked an exited session for its panes")
		}
	}
}
