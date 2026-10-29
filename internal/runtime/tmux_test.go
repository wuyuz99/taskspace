package runtime

import (
	"context"
	"strings"
	"testing"

	tsexec "github.com/wuyuz99/taskspace/internal/exec"
)

func ctx() context.Context { return context.Background() }

func TestSessionNameSanitizesTmuxTargetSyntax(t *testing.T) {
	tm := NewTmux(tsexec.NewFake())
	cases := map[string]string{
		"checkout-redesign": "tspace@checkout-redesign",
		// '.' and ':' are tmux target syntax; leaving them in breaks every -t.
		"checkout.redesign": "tspace@checkout~2eredesign",
		"payments_fix":      "tspace@payments_fix",
		"weird name":        "tspace@weird~20name",
	}
	for in, want := range cases {
		if got := tm.SessionName(in); got != want {
			t.Errorf("SessionName(%q) = %q, want %q", in, got, want)
		}
	}
}

// Prefix matching would make "tspace-pay" select the session "tspace-payments-fix".
// Every target we build must be an exact match.
func TestTargetsUseExactMatchPrefix(t *testing.T) {
	f := tsexec.NewFake()
	tm := NewTmux(f)
	if _, err := tm.HasSession(ctx(), "tspace-pay"); err != nil {
		t.Fatalf("HasSession: %v", err)
	}
	cmds := f.Commands()
	if len(cmds) != 1 {
		t.Fatalf("commands = %v, want one", cmds)
	}
	if !strings.Contains(cmds[0], "-t =tspace-pay") {
		t.Errorf("command = %q, want an exact-match target '=tspace-pay'", cmds[0])
	}
}

func TestStartCreatesSessionThenWindows(t *testing.T) {
	f := tsexec.NewFake()
	f.Fail("has-session", "no server running", 1)
	tm := NewTmux(f)
	tm.Shell = "/bin/bash"

	windows := []Window{
		{Name: "backend", Dir: "/wt/task/backend", Command: []string{"claude"}},
		{Name: "frontend", Dir: "/wt/task/frontend", Command: []string{"codex", "--flag"}},
		{Name: "infra", Dir: "/wt/task/infra"},
	}
	if err := tm.Start(ctx(), "tspace-task", windows); err != nil {
		t.Fatalf("Start: %v", err)
	}
	cmds := strings.Join(f.Commands(), "\n")

	if !strings.Contains(cmds, "new-session -d -s tspace-task -n backend -c /wt/task/backend /bin/sh") || !strings.Contains(cmds, "respawn-pane -k -t =tspace-task:=backend -c /wt/task/backend claude") {
		t.Errorf("missing correct new-session; got:\n%s", cmds)
	}
	if !strings.Contains(cmds, "respawn-pane -k -t =tspace-task:=frontend -c /wt/task/frontend codex --flag") {
		t.Errorf("missing correct new-window for frontend; got:\n%s", cmds)
	}
	// A window with no agent gets an interactive shell.
	if !strings.Contains(cmds, "respawn-pane -k -t =tspace-task:=infra -c /wt/task/infra /bin/bash") {
		t.Errorf("agent-less window did not launch a shell; got:\n%s", cmds)
	}
	// Without remain-on-exit, a crashed agent's traceback disappears with the window.
	if strings.Count(cmds, "remain-on-exit on") != 3 {
		t.Errorf("remain-on-exit not set on every window; got:\n%s", cmds)
	}
}

func TestStartReusesExistingSessionAndAddsOnlyMissingWindows(t *testing.T) {
	f := tsexec.NewFake()
	f.RespondOut("list-panes", "backend\t0\tclaude\n")
	tm := NewTmux(f)

	windows := []Window{
		{Name: "backend", Dir: "/wt/backend", Command: []string{"claude"}},
		{Name: "frontend", Dir: "/wt/frontend", Command: []string{"codex"}},
	}
	if err := tm.Start(ctx(), "tspace-task", windows); err != nil {
		t.Fatalf("Start: %v", err)
	}
	cmds := strings.Join(f.Commands(), "\n")

	if strings.Contains(cmds, "new-session") {
		t.Errorf("Start recreated an existing session; got:\n%s", cmds)
	}
	// backend is already running: recreating it would kill the agent.
	if strings.Contains(cmds, "-n backend") {
		t.Errorf("Start recreated the running backend window; got:\n%s", cmds)
	}
	if !strings.Contains(cmds, "-n frontend") {
		t.Errorf("Start did not add the missing frontend window; got:\n%s", cmds)
	}
}

// Every hasWindow probe costs a has-session plus a list-panes, and one
// list-panes already describes every window in the session.
func TestStartQueriesSessionStateOnce(t *testing.T) {
	f := tsexec.NewFake()
	f.RespondOut("list-panes", "backend\t0\tclaude\n")
	tm := NewTmux(f)

	windows := []Window{
		{Name: "backend", Dir: "/wt/backend"},
		{Name: "frontend", Dir: "/wt/frontend"},
		{Name: "infra", Dir: "/wt/infra"},
	}
	if err := tm.Start(ctx(), "tspace-task", windows); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if n := strings.Count(strings.Join(f.Commands(), "\n"), "list-panes"); n != 1 {
		t.Errorf("list-panes ran %d times, want 1: %v", n, f.Commands())
	}
}

// tmux happily creates a second window with the same name, and the respawn that
// follows would then land on the first one — killing a running agent.
func TestOpenWindowRefusesAnExistingName(t *testing.T) {
	f := tsexec.NewFake()
	f.RespondOut("list-panes", "backend\t0\tclaude\n")
	tm := NewTmux(f)

	err := tm.OpenWindow(ctx(), "tspace-task", Window{Name: "backend", Dir: "/wt/backend"})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("OpenWindow on a live window = %v, want refusal", err)
	}
	if f.Ran("new-window") || f.Ran("respawn-window") || f.Ran("respawn-pane") {
		t.Errorf("refusal still touched the window: %v", f.Commands())
	}

	if err := tm.OpenWindow(ctx(), "tspace-task", Window{Name: "frontend", Dir: "/wt/frontend"}); err != nil {
		t.Fatalf("OpenWindow on a free name: %v", err)
	}
	if !f.Ran("new-window -d -t =tspace-task -n frontend") {
		t.Errorf("free name was not created: %v", f.Commands())
	}
}

// A shell the user exits has nothing worth keeping on screen; an agent that
// crashes does. Only the latter keeps its pane after exit.
func TestTransientWindowClosesOnExit(t *testing.T) {
	f := tsexec.NewFake()
	tm := NewTmux(f)
	if err := tm.OpenWindow(ctx(), "tspace-task", Window{Name: "_staging", Dir: "/wt", Transient: true}); err != nil {
		t.Fatalf("OpenWindow: %v", err)
	}
	if f.Ran("remain-on-exit") {
		t.Errorf("transient window kept on exit: %v", f.Commands())
	}
	if !f.Ran("automatic-rename off") {
		t.Errorf("transient window can be renamed away from its target: %v", f.Commands())
	}
	if err := tm.OpenWindow(ctx(), "tspace-task", Window{Name: "_logs", Dir: "/wt", Command: []string{"tail", "-f", "log"}}); err != nil {
		t.Fatalf("OpenWindow: %v", err)
	}
	if !f.Ran("-t =tspace-task:=_logs -w remain-on-exit on") {
		t.Errorf("command window not kept on exit: %v", f.Commands())
	}
}

func TestFocusOnlyFromInsideTmux(t *testing.T) {
	f := tsexec.NewFake()
	tm := NewTmux(f)
	t.Setenv("TMUX", "")
	if ok, err := tm.Focus(ctx(), "tspace-task", "_staging"); ok || err != nil || f.Ran("switch-client") {
		t.Errorf("Focus outside tmux = %v, %v; commands %v", ok, err, f.Commands())
	}
	t.Setenv("TMUX", "/tmp/tmux-1000/default,1,0")
	if ok, err := tm.Focus(ctx(), "tspace-task", "_staging"); !ok || err != nil || !f.Ran("switch-client -t =tspace-task:=_staging") {
		t.Errorf("Focus inside tmux = %v, %v; commands %v", ok, err, f.Commands())
	}
}

func TestSelectTargetsTheWindow(t *testing.T) {
	f := tsexec.NewFake()
	if err := NewTmux(f).Select(ctx(), "tspace-task", "api.v1"); err != nil || !f.Ran("select-window -t =tspace-task:=api~2ev1") {
		t.Errorf("Select = %v; commands %v", err, f.Commands())
	}
}

func TestStartRejectsEmptyWindowList(t *testing.T) {
	tm := NewTmux(tsexec.NewFake())
	if err := tm.Start(ctx(), "tspace-task", nil); err == nil {
		t.Fatal("Start with no windows = nil, want error")
	}
}

// A window target resolves to the window's *active* pane, so a split opened to
// probe something would never see the interrupt and would be hard-killed
// instead. Stop must address panes individually.
func TestStopInterruptsEveryPaneBeforeKilling(t *testing.T) {
	f := tsexec.NewFake()
	// Three panes: backend, backend's split, and frontend.
	f.RespondOut("list-panes", "%0\t0\n%1\t0\n%2\t0\n")
	tm := NewTmux(f)
	tm.StopGrace = 0 // keep the test fast; ordering is what matters

	if err := tm.Stop(ctx(), "tspace-task"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	cmds := f.Commands()
	joined := strings.Join(cmds, "\n")

	if strings.Count(joined, "send-keys") != 3 {
		t.Errorf("want C-c to each live pane, splits included; got:\n%s", joined)
	}
	for _, pane := range []string{"%0", "%1", "%2"} {
		if !strings.Contains(joined, "send-keys -t "+pane+" C-c") {
			t.Errorf("missing interrupt for pane %s; got:\n%s", pane, joined)
		}
	}
	if strings.Contains(joined, "send-keys -t =tspace-task") {
		t.Errorf("interrupt addressed a window instead of a pane; got:\n%s", joined)
	}
	if !strings.Contains(joined, "kill-session -t =tspace-task") {
		t.Errorf("missing kill-session; got:\n%s", joined)
	}

	var lastSend, kill int
	for i, c := range cmds {
		if strings.Contains(c, "send-keys") {
			lastSend = i
		}
		if strings.Contains(c, "kill-session") {
			kill = i
		}
	}
	if kill < lastSend {
		t.Errorf("kill-session ran before the interrupts; got:\n%s", joined)
	}
}

func TestStopOnMissingSessionIsANoOp(t *testing.T) {
	f := tsexec.NewFake()
	f.Fail("has-session", "can't find session", 1)
	tm := NewTmux(f)

	if err := tm.Stop(ctx(), "tspace-gone"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if f.Ran("kill-session") {
		t.Errorf("Stop killed something despite no session; got: %v", f.Commands())
	}
}

func TestStopSkipsDeadPanes(t *testing.T) {
	f := tsexec.NewFake()
	f.RespondOut("list-panes", "%0\t1\n%1\t0\n")
	tm := NewTmux(f)
	tm.StopGrace = 0

	if err := tm.Stop(ctx(), "tspace-task"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	joined := strings.Join(f.Commands(), "\n")
	if strings.Contains(joined, "-t %0 C-c") {
		t.Errorf("interrupted an already-dead pane; got:\n%s", joined)
	}
	if !strings.Contains(joined, "-t %1 C-c") {
		t.Errorf("did not interrupt the live pane; got:\n%s", joined)
	}
}

func TestStateUsesFormatStringsNotHumanOutput(t *testing.T) {
	f := tsexec.NewFake()
	f.RespondOut("list-panes", "backend\t0\tclaude\nfrontend\t1\tbash\n")
	tm := NewTmux(f)

	st, err := tm.State(ctx(), "tspace-task")
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if !st.Exists {
		t.Fatal("Exists = false, want true")
	}
	if len(st.Panes) != 2 {
		t.Fatalf("Panes = %+v, want 2", st.Panes)
	}
	if p, _ := st.Pane("backend"); p.Dead {
		t.Error("backend reported dead, want alive")
	}
	if p, _ := st.Pane("frontend"); !p.Dead {
		t.Error("frontend reported alive, want dead")
	}
	if !strings.Contains(strings.Join(f.Commands(), "\n"), "-F #{window_name}") {
		t.Error("State did not request a format string")
	}
}

func TestStateOnMissingSession(t *testing.T) {
	f := tsexec.NewFake()
	f.Fail("has-session", "can't find session", 1)
	tm := NewTmux(f)

	st, err := tm.State(ctx(), "tspace-gone")
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if st.Exists {
		t.Error("Exists = true for a missing session")
	}
	if len(st.Panes) != 0 {
		t.Errorf("Panes = %+v, want empty", st.Panes)
	}
}

// A user splitting a window to probe something must not make the agent look
// dead, so a window is alive if any of its panes is alive.
func TestParsePanesCollapsesSplitsOptimistically(t *testing.T) {
	panes := parsePanes("backend\t1\tclaude\nbackend\t0\tbash\n")
	if len(panes) != 1 {
		t.Fatalf("panes = %+v, want 1 window", panes)
	}
	if panes["backend"].Dead {
		t.Error("window reported dead although one pane is alive")
	}

	allDead := parsePanes("backend\t1\tclaude\nbackend\t1\tbash\n")
	if !allDead["backend"].Dead {
		t.Error("window with only dead panes reported alive")
	}
}

func TestParsePanesIgnoresMalformedLines(t *testing.T) {
	panes := parsePanes("\nbackend\n\nfrontend\t0\tcodex\n")
	if len(panes) != 1 {
		t.Fatalf("panes = %+v, want only the well-formed line", panes)
	}
	if _, ok := panes["frontend"]; !ok {
		t.Error("well-formed line was dropped")
	}
}

func TestLaunchArgvFallsBackToShell(t *testing.T) {
	tm := NewTmux(tsexec.NewFake())
	tm.Shell = "/bin/zsh"
	if got := tm.launchArgv(Window{Name: "infra"}); len(got) != 1 || got[0] != "/bin/zsh" {
		t.Errorf("launchArgv = %v, want [/bin/zsh]", got)
	}
	if got := tm.launchArgv(Window{Name: "backend", Command: []string{"claude", "-p"}}); strings.Join(got, " ") != "claude -p" {
		t.Errorf("launchArgv = %v, want [claude -p]", got)
	}
}

func TestLaunchArgvSetsWindowEnv(t *testing.T) {
	tm := NewTmux(tsexec.NewFake())
	tm.Shell = "/bin/zsh"
	w := Window{Name: "infra", Env: []string{"TSPACE_TASK=PAY-142", "TSPACE_WINDOW=infra"}}
	if got := strings.Join(tm.launchArgv(w), " "); got != "env TSPACE_TASK=PAY-142 TSPACE_WINDOW=infra /bin/zsh" {
		t.Errorf("launchArgv = %q", got)
	}
	w.Command = []string{"claude", "-p"}
	if got := strings.Join(tm.launchArgv(w), " "); got != "env TSPACE_TASK=PAY-142 TSPACE_WINDOW=infra claude -p" {
		t.Errorf("launchArgv = %q", got)
	}
}

// tmux exits 1 for every failure; only "no such session" means missing.
func TestHasSessionOnlyTrustsAMissingSession(t *testing.T) {
	for stderr, missing := range map[string]bool{
		"can't find session: tspace@x":                                           true,
		"no server running on /tmp/tmux-1000/default":                            true,
		"error connecting to /tmp/tmux-1000/default (No such file or directory)": true,
		"protocol version mismatch (client 8, server 7)":                         false,
		"error connecting to /tmp/tmux-1000/default (Permission denied)":         false,
	} {
		f := tsexec.NewFake().Fail("has-session", stderr, 1)
		ok, err := NewTmux(f).HasSession(ctx(), "tspace@x")
		if ok || (err == nil) != missing {
			t.Errorf("%q: HasSession = %v, %v", stderr, ok, err)
		}
	}
}
