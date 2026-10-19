package cli

import (
	"os"
	"slices"
	"strings"
	"testing"
)

func TestTerminalArgv(t *testing.T) {
	cmd := []string{"/bin/tspace", "open", "PAY-1"}
	if got := terminalArgv([]string{"wezterm", "cli", "spawn", "--"}, cmd); !slices.Equal(got, []string{"wezterm", "cli", "spawn", "--", "/bin/tspace", "open", "PAY-1"}) {
		t.Errorf("appended: %q", got)
	}
	got := terminalArgv([]string{"osascript", "-e", `do script "{command}"`}, cmd)
	if len(got) != 3 || got[2] != `do script "/bin/tspace open PAY-1"` {
		t.Errorf("placed: %q", got)
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
	if err := h.exec(t, h.app.newConfigCmd(), "terminal", "--unset"); err != nil {
		t.Fatal(err)
	}
}
