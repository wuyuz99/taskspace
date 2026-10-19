package runtime

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	tsexec "github.com/wuyuz99/taskspace/internal/exec"
)

// zellijPanesJSON is trimmed `zellij action list-panes --all --json` output:
// a plugin pane, a live agent, and a held pane whose command, codex in /wt/b,
// exited.
var zellijPanesJSON = fmt.Sprintf(`[
  {"id":1,"is_plugin":true,"exited":false,"is_held":false,"tab_id":0,"tab_position":0,"tab_name":"_root"},
  {"id":0,"is_plugin":false,"exited":false,"is_held":false,"tab_id":0,"tab_position":0,"tab_name":"_root","pane_command":"/usr/bin/claude --model opus","terminal_command":"claude --model opus"},
  {"id":2,"is_plugin":false,"exited":true,"is_held":true,"tab_id":1,"tab_position":1,"tab_name":"backend","pane_command":null,"terminal_command":%q}
]`, strings.Join(NewZellij(nil).launchArgv(backend), " "))

var backend = Window{Name: "backend", Dir: "/wt/b", Command: []string{"codex"}}

// terminalPanes is list-panes output holding one live terminal pane per tab.
func terminalPanes(tabs ...string) string {
	var panes []string
	for i, tab := range tabs {
		panes = append(panes, fmt.Sprintf(`{"id":%d,"is_plugin":false,"exited":false,"tab_id":%d,"tab_position":%d,"tab_name":%q,"pane_command":"sh"}`, i, i, i, tab))
	}
	return "[" + strings.Join(panes, ",") + "]"
}

func zellijWith(f *tsexec.Fake) *Zellij {
	z := NewZellij(f)
	z.StopGrace = 0
	z.Shell = "/bin/bash"
	return z
}

func TestZellijSessionsIgnoreExited(t *testing.T) {
	got := parseSessions("tspace@a [Created 3m ago] \ntspace@b [Created 2h ago] (EXITED - attach to resurrect)\n")
	if e, ok := got["tspace@a"]; !ok || e.exited {
		t.Errorf("tspace@a = %+v, %v; want running", e, ok)
	}
	if e, ok := got["tspace@b"]; !ok || !e.exited {
		t.Errorf("tspace@b = %+v, %v; want exited", e, ok)
	}

	f := tsexec.NewFake().RespondOut("list-sessions", "tspace@b [Created 2h ago] (EXITED - attach to resurrect)\n")
	if ok, err := zellijWith(f).HasSession(ctx(), "tspace@b"); err != nil || ok {
		t.Errorf("HasSession(exited) = %v, %v; want false", ok, err)
	}
}

func TestZellijNoSessionsIsNotAnError(t *testing.T) {
	f := tsexec.NewFake().Fail("list-sessions", "No active zellij sessions found.", 1)
	ok, err := zellijWith(f).HasSession(ctx(), "tspace@a")
	if err != nil || ok {
		t.Errorf("HasSession = %v, %v; want false, nil", ok, err)
	}
}

func TestZellijStateGroupsPanesByTab(t *testing.T) {
	f := tsexec.NewFake().
		RespondOut("list-sessions", "tspace@t [Created 1s ago]\n").
		RespondOut("list-panes", zellijPanesJSON)
	st, err := zellijWith(f).State(ctx(), "tspace@t")
	if err != nil {
		t.Fatal(err)
	}
	if !st.Exists || len(st.Panes) != 2 {
		t.Fatalf("state = %+v; want two windows (plugin panes are not windows)", st)
	}
	if p := st.Panes["_root"]; p.Dead || p.Command != "claude" {
		t.Errorf("_root = %+v; want live claude", p)
	}
	if p := st.Panes["backend"]; !p.Dead {
		t.Errorf("backend = %+v; want dead", p)
	}
	if !f.Ran("--session tspace@t action list-panes --all --json") {
		t.Errorf("list-panes not addressed to the session: %v", f.Commands())
	}
}

func TestZellijStartCreatesSessionFromOneLayout(t *testing.T) {
	f := tsexec.NewFake().Fail("list-sessions", "No active zellij sessions found.", 1).RespondOut("list-panes", terminalPanes("_root", "a~2eb"))
	var layout string
	z := zellijWith(f)
	windows := []Window{
		{Name: "_root", Dir: "/wt/t", Command: []string{"claude", `say "hi"`}},
		{Name: "a.b", Dir: "/wt/t/a.b"},
	}
	layout = z.layout(windows)
	if err := z.Start(ctx(), "tspace@t", windows); err != nil {
		t.Fatal(err)
	}
	cmds := strings.Join(f.Commands(), "\n")
	if !strings.Contains(cmds, "attach --create-background tspace@t") {
		t.Errorf("no background session created:\n%s", cmds)
	}
	// Tabs added to a detached session are lost, so nothing is added afterwards.
	if strings.Contains(cmds, "new-tab") {
		t.Errorf("windows added with new-tab instead of the layout:\n%s", cmds)
	}
	for _, want := range []string{
		`tab name="_root" cwd="/wt/t"`,
		`pane command="sh"`,
		`args "-c" "sleep 0.25; exec \"$@\"" "tspace-`,
		`" "claude" "say \"hi\""`,
		`tab name="a~2eb" cwd="/wt/t/a.b"`,
		`" "/bin/bash"`,
		`zellij:tab-bar`,
	} {
		if !strings.Contains(layout, want) {
			t.Errorf("layout lacks %s:\n%s", want, layout)
		}
	}
}

func TestZellijStartWaitsForEveryPane(t *testing.T) {
	// The tab names are up, but only _root has its pane yet.
	f := tsexec.NewFake().Fail("list-sessions", "No active zellij sessions found.", 1).
		RespondOut("query-tab-names", "_root\nb\n").
		RespondOut("list-panes", terminalPanes("_root"))
	z := zellijWith(f)
	c, cancel := context.WithTimeout(ctx(), 200*time.Millisecond)
	defer cancel()
	if err := z.Start(c, "tspace@t", []Window{{Name: "_root"}, {Name: "b"}}); err == nil {
		t.Fatal("Start returned before every window had its pane")
	}
}

func TestZellijStartReplacesExitedSession(t *testing.T) {
	f := tsexec.NewFake().RespondOut("list-sessions", "tspace@t [Created 1d ago] (EXITED - attach to resurrect)\n").
		RespondOut("list-panes", terminalPanes("_root"))
	if err := zellijWith(f).Start(ctx(), "tspace@t", []Window{{Name: "_root"}}); err != nil {
		t.Fatal(err)
	}
	cmds := f.Commands()
	del, create := -1, -1
	for i, c := range cmds {
		if strings.Contains(c, "delete-session tspace@t") {
			del = i
		}
		if strings.Contains(c, "attach --create-background tspace@t") {
			create = i
		}
	}
	if del < 0 || create < del {
		t.Errorf("want delete-session before create, got %v", cmds)
	}
}

func TestZellijAddsTabsOnlyWithAClient(t *testing.T) {
	base := func() *tsexec.Fake {
		return tsexec.NewFake().
			RespondOut("list-sessions", "tspace@t [Created 1s ago]\n").
			RespondOut("list-panes", zellijPanesJSON)
	}
	windows := []Window{{Name: "_root"}, {Name: "backend"}, {Name: "frontend", Dir: "/wt/f", Command: []string{"codex"}}}

	f := base().RespondOut("list-clients", "CLIENT_ID ZELLIJ_PANE_ID RUNNING_COMMAND\n")
	if err := zellijWith(f).Start(ctx(), "tspace@t", windows); err == nil || !strings.Contains(err.Error(), "attach first") {
		t.Errorf("Start without a client = %v; want a refusal", err)
	}
	if f.Ran("new-tab") {
		t.Errorf("tab added to a detached session: %v", f.Commands())
	}

	f = base().RespondOut("list-clients", "CLIENT_ID ZELLIJ_PANE_ID RUNNING_COMMAND\n1 terminal_0 claude\n")
	if err := zellijWith(f).Start(ctx(), "tspace@t", windows); err != nil {
		t.Fatal(err)
	}
	cmds := strings.Join(f.Commands(), "\n")
	if strings.Count(cmds, "new-tab") != 1 || !strings.Contains(cmds, "--session tspace@t action new-tab --no-focus --name frontend --cwd /wt/f -- sh -c sleep 0.25; exec \"$@\" tspace-") ||
		!strings.HasSuffix(cmds, " codex") {
		t.Errorf("want only the missing frontend tab, got:\n%s", cmds)
	}
}

func TestZellijOpenWindowRefusesDuplicate(t *testing.T) {
	f := tsexec.NewFake().
		RespondOut("list-sessions", "tspace@t [Created 1s ago]\n").
		RespondOut("list-panes", zellijPanesJSON)
	if err := zellijWith(f).OpenWindow(ctx(), "tspace@t", Window{Name: "backend"}); err == nil {
		t.Error("OpenWindow over an existing tab succeeded")
	}
}

func TestZellijRestartRerunsHeldPaneInPlace(t *testing.T) {
	f := tsexec.NewFake().
		RespondOut("list-sessions", "tspace@t [Created 1s ago]\n").
		RespondOut("list-panes", zellijPanesJSON)
	z := zellijWith(f)
	if err := z.Restart(ctx(), "tspace@t", Window{Name: "_root", Command: []string{"claude", "--model", "opus"}}); err == nil {
		t.Error("Restart of a live window succeeded")
	}
	if err := z.Restart(ctx(), "tspace@t", backend); err != nil {
		t.Fatal(err)
	}
	if !f.Ran("--session tspace@t action send-keys --pane-id terminal_2 Enter") {
		t.Errorf("held pane not rerun: %v", f.Commands())
	}

	// A pane launched before tspace set a variable is still the same launch.
	newer := backend
	newer.Env = append([]string{}, backend.Env...)
	newer.Env = append(newer.Env, "TSPACE_ROOT=/state")
	before := len(f.Commands())
	if err := z.Restart(ctx(), "tspace@t", newer); err != nil {
		t.Fatal(err)
	}
	if got := f.Commands()[before:]; !strings.Contains(strings.Join(got, " "), "send-keys --pane-id terminal_2 Enter") {
		t.Errorf("older pane not rerun in place: %v", got)
	}

	// A changed command means a new tab, which needs a client.
	f.RespondOut("list-clients", "CLIENT_ID ZELLIJ_PANE_ID RUNNING_COMMAND\n")
	if err := z.Restart(ctx(), "tspace@t", Window{Name: "backend", Dir: "/wt/b", Command: []string{"codex", "--new"}}); err == nil {
		t.Error("Restart with a changed command and no client succeeded")
	}
	// So does a changed directory: rerunning in place would use the old one.
	if err := z.Restart(ctx(), "tspace@t", Window{Name: "backend", Dir: "/wt/moved", Command: []string{"codex"}}); err == nil {
		t.Error("Restart with a changed directory and no client succeeded")
	}
}

func TestZellijLaunchFingerprintKeepsArgvApart(t *testing.T) {
	z := zellijWith(tsexec.NewFake())
	a := strings.Join(z.launchArgv(Window{Command: []string{"a b"}}), " ")
	b := strings.Join(z.launchArgv(Window{Command: []string{"a", "b"}}), " ")
	if a == b {
		t.Errorf("[\"a b\"] and [\"a\" \"b\"] launch as the same command line: %s", a)
	}
}

func TestZellijStateListsSessionsOnlyWhenNeeded(t *testing.T) {
	f := tsexec.NewFake().RespondOut("list-panes", zellijPanesJSON)
	if _, err := zellijWith(f).State(ctx(), "tspace@t"); err != nil {
		t.Fatal(err)
	}
	if f.Ran("list-sessions") {
		t.Errorf("list-sessions run although list-panes answered: %v", f.Commands())
	}

	f = tsexec.NewFake().Fail("list-panes", "Session not found", 1).RespondOut("list-sessions", "tspace@other [Created 1s ago]\n")
	st, err := zellijWith(f).State(ctx(), "tspace@t")
	if err != nil || st.Exists {
		t.Errorf("State of a missing session = %+v, %v; want missing, nil", st, err)
	}
}

func TestZellijCreateFromInsideZellijIsNotNested(t *testing.T) {
	t.Setenv("ZELLIJ", "0")
	t.Setenv("ZELLIJ_SESSION_NAME", "elsewhere")
	f := tsexec.NewFake().Fail("list-sessions", "No active zellij sessions found.", 1).RespondOut("list-panes", terminalPanes("_root"))
	if err := zellijWith(f).Start(ctx(), "tspace@t", []Window{{Name: "_root"}}); err != nil {
		t.Fatal(err)
	}
	if !f.Ran("env -u ZELLIJ -u ZELLIJ_SESSION_NAME -u ZELLIJ_PANE_ID zellij --layout") {
		t.Errorf("session created with the current session's environment: %v", f.Commands())
	}
}

func TestZellijStopInterruptsLivePanesThenDeletes(t *testing.T) {
	f := tsexec.NewFake().
		RespondOut("list-sessions", "tspace@t [Created 1s ago]\n").
		RespondOut("list-panes", zellijPanesJSON)
	if err := zellijWith(f).Stop(ctx(), "tspace@t"); err != nil {
		t.Fatal(err)
	}
	cmds := strings.Join(f.Commands(), "\n")
	if !strings.Contains(cmds, "send-keys --pane-id terminal_0 Ctrl c") || strings.Contains(cmds, "terminal_2 Ctrl c") {
		t.Errorf("want Ctrl-C to the live pane only:\n%s", cmds)
	}
	if !strings.Contains(cmds, "kill-session tspace@t") || !strings.Contains(cmds, "delete-session tspace@t") {
		t.Errorf("session not killed and deleted:\n%s", cmds)
	}
}

func TestZellijFocusOutsideZellijDoesNothing(t *testing.T) {
	t.Setenv("ZELLIJ", "")
	f := tsexec.NewFake()
	ok, err := zellijWith(f).Focus(ctx(), "tspace@t", "backend")
	if err != nil || ok || len(f.Calls()) != 0 {
		t.Errorf("Focus outside zellij = %v, %v, calls %v", ok, err, f.Commands())
	}
}

func TestZellijFocusSwitchesSessionInsideZellij(t *testing.T) {
	t.Setenv("ZELLIJ", "0")
	t.Setenv("ZELLIJ_SESSION_NAME", "elsewhere")
	f := tsexec.NewFake().
		RespondOut("list-sessions", "tspace@t [Created 1s ago]\n").
		RespondOut("list-panes", zellijPanesJSON)
	ok, err := zellijWith(f).Focus(ctx(), "tspace@t", "backend")
	if err != nil || !ok {
		t.Fatalf("Focus = %v, %v", ok, err)
	}
	if !f.Ran("zellij action switch-session tspace@t --tab-position 1") {
		t.Errorf("no switch-session: %v", f.Commands())
	}
}

func TestZellijFocusRefusesMissingTabInCurrentSession(t *testing.T) {
	t.Setenv("ZELLIJ", "0")
	t.Setenv("ZELLIJ_SESSION_NAME", "tspace@t")
	f := tsexec.NewFake().RespondOut("list-panes", zellijPanesJSON)
	ok, err := zellijWith(f).Focus(ctx(), "tspace@t", "missing")
	if err == nil || ok {
		t.Errorf("Focus(missing) = %v, %v; want an error", ok, err)
	}
	if f.Ran("go-to-tab-name") {
		t.Errorf("switched to a tab that does not exist: %v", f.Commands())
	}
}

func TestZellijAttachInsideZellijLandsOnSelectedTab(t *testing.T) {
	t.Setenv("ZELLIJ", "0")
	t.Setenv("ZELLIJ_SESSION_NAME", "elsewhere")
	f := tsexec.NewFake().
		RespondOut("list-sessions", "tspace@t [Created 1s ago]\n").
		RespondOut("list-panes", zellijPanesJSON)
	z := zellijWith(f)
	if err := z.Select(ctx(), "tspace@t", "backend"); err != nil {
		t.Fatal(err)
	}
	if err := z.Attach(ctx(), "tspace@t"); err != nil {
		t.Fatal(err)
	}
	if !f.Ran("zellij action switch-session tspace@t --tab-position 1") {
		t.Errorf("switch-session ignored the selected tab: %v", f.Commands())
	}
	if w := z.takeSelected("tspace@t"); w != "" {
		t.Errorf("selection %q left behind after Attach", w)
	}
}

func TestKDLStringEscapes(t *testing.T) {
	if got := kdlString("a\"b\\c\nd\x01"); got != `"a\"b\\c\nd\u{1}"` {
		t.Errorf("kdlString = %s", got)
	}
}

func TestMain(m *testing.M) {
	// Tests must not see the zellij session they might be running inside.
	os.Unsetenv("ZELLIJ")
	os.Unsetenv("ZELLIJ_SESSION_NAME")
	os.Exit(m.Run())
}
