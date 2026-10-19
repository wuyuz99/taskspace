package cli

import (
	"os"
	"slices"
	"strings"
	"testing"
)

func TestTerminalArgv(t *testing.T) {
	cmd := []string{"/bin/tspace", "attach", "PAY-1"}
	if got := terminalArgvFor("linux", []string{"wezterm", "cli", "spawn", "--"}, cmd); !slices.Equal(got, []string{"wezterm", "cli", "spawn", "--", "/bin/tspace", "attach", "PAY-1"}) {
		t.Errorf("appended: %q", got)
	}
	got := terminalArgvFor("darwin", []string{"osascript", "-e", `do script "{command}"`}, cmd)
	if len(got) != 3 || got[2] != `do script "/bin/tspace attach PAY-1"` {
		t.Errorf("placed: %q", got)
	}
	// Quoted for cmd.exe on Windows, not POSIX-style.
	win := terminalArgvFor("windows", []string{"wt.exe", "new-tab", "cmd", "/c", "{command}"}, []string{`C:\Program Files\tspace.exe`, "attach", "PAY-1"})
	if got := win[4]; got != `"C:\Program Files\tspace.exe" attach PAY-1` {
		t.Errorf("windows: %q", got)
	}
}

// The configured terminal is run with tspace open TASK; without one, the
// error says how to set it.
func TestOpenInNewTab(t *testing.T) {
	h := newHarness(t)
	if err := h.app.openInNewTab(t.Context(), "PAY-1"); err == nil || !strings.Contains(err.Error(), "tspace config terminal") {
		t.Fatalf("without a terminal = %v", err)
	}
	out := fakeShell(t) // the test binary records what it was run with
	self, _ := os.Executable()
	if err := h.exec(t, h.app.newConfigCmd(), "terminal", self); err != nil {
		t.Fatal(err)
	}
	if err := h.app.openInNewTab(t.Context(), "PAY-1"); err != nil {
		t.Fatal(err)
	}
	env := readShellEnv(t, out)
	if !samePath(env["TSPACE_ROOT"], h.app.Store.Root()) {
		t.Errorf("the tab's TSPACE_ROOT = %q", env["TSPACE_ROOT"])
	}
	if !strings.HasSuffix(env["ARGS"], " attach PAY-1") {
		t.Errorf("the terminal ran %q", env["ARGS"])
	}
	// A --unset after the command would be saved as part of it.
	if err := h.exec(t, h.app.newConfigCmd(), "terminal", "kitty", "--unset"); err == nil {
		t.Error("--unset after the command accepted")
	}
}

// The tab does not inherit what says where dash runs.
func TestNewTabEnv(t *testing.T) {
	env := newTabEnv([]string{"TMUX=/tmp/x,1,0", "ZELLIJ=0", "TSPACE_TASK=PAY-1", "TSPACE_WINDOW=_root", "TSPACE_ROOT=/old", "HOME=/h"}, "/state")
	if !slices.Equal(env, []string{"HOME=/h", "TSPACE_ROOT=/state"}) {
		t.Fatalf("env %q", env)
	}
}
