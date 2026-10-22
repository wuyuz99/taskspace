package runtime

import (
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	tsexec "github.com/wuyuz99/taskspace/internal/exec"
)

// Exercises the zellij behaviours this runtime is built around — a session
// created from one layout, held panes, rerun in place — against a real
// server. zellij has no private-socket option, so the session gets a name no
// person would use.
func TestRealZellijLifecycle(t *testing.T) {
	if _, err := exec.LookPath("zellij"); err != nil {
		t.Skip("zellij unavailable")
	}
	z := NewZellij(tsexec.Real())
	z.StopGrace = 0
	if err := z.Available(ctx()); err != nil {
		t.Skip(err)
	}
	t.Setenv("ZELLIJ", "")
	dir := t.TempDir()
	s := fmt.Sprintf("tspace@zz-test-%d", os.Getpid())
	t.Cleanup(func() {
		exec.Command("zellij", "kill-session", s).Run()
		exec.Command("zellij", "delete-session", s).Run()
	})

	flag := dir + "/go"
	windows := []Window{
		{Name: "_root", Dir: dir, Command: []string{"sleep", "60"}},
		// Exits at first; once the flag exists, a rerun stays up.
		{Name: "api.v1", Dir: dir, Command: []string{"sh", "-c", "test -f " + flag + " && exec sleep 60; exit 3"}},
	}
	if err := z.Start(ctx(), s, windows); err != nil {
		t.Fatal(err)
	}
	// Start returns only once every window can be seen, so a status or a
	// second start straight away finds them all.
	if st, err := z.State(ctx(), s); err != nil || len(missingWindows(st, windows)) != 0 {
		t.Fatalf("windows missing right after Start: %+v, %v", st, err)
	}
	waitFor := func(what string, ok func(SessionState) bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			st, err := z.State(ctx(), s)
			if err != nil {
				t.Fatal(err)
			}
			if ok(st) {
				return
			}
			if time.Now().After(deadline) {
				// zellij's own view, for diagnosing a CI-only failure.
				panes, _ := exec.Command("zellij", "--session", s, "action", "list-panes", "--all", "--json").CombinedOutput()
				tabs, _ := exec.Command("zellij", "--session", s, "action", "list-tabs", "--all", "--json").CombinedOutput()
				t.Fatalf("%s: %+v\npanes: %s\ntabs: %s", what, st, panes, tabs)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	waitFor("exited command not held", func(st SessionState) bool {
		p, ok := st.Panes[WindowName("api.v1")]
		return ok && p.Dead && !st.Panes["_root"].Dead
	})
	if err := z.Start(ctx(), s, windows); err != nil {
		t.Fatalf("second Start: %v", err)
	}
	if err := z.Restart(ctx(), s, windows[0]); err == nil {
		t.Fatal("live window restarted")
	}
	if err := os.WriteFile(flag, nil, 0644); err != nil {
		t.Fatal(err)
	}
	if err := z.Restart(ctx(), s, windows[1]); err != nil {
		t.Fatal(err)
	}
	waitFor("held pane not rerun", func(st SessionState) bool {
		return !st.Panes[WindowName("api.v1")].Dead
	})
	if err := z.OpenWindow(ctx(), s, Window{Name: "_x", Dir: dir}); err == nil {
		t.Fatal("tab added to a session with no client")
	}
	// Closing one window leaves the other, and the session, running.
	if err := z.CloseWindow(ctx(), s, "api.v1"); err != nil {
		t.Fatal(err)
	}
	waitFor("window not closed", func(st SessionState) bool {
		_, open := st.Panes[WindowName("api.v1")]
		_, root := st.Panes["_root"]
		return st.Exists && !open && root
	})
	if err := z.Stop(ctx(), s); err != nil {
		t.Fatal(err)
	}
	if ok, err := z.HasSession(ctx(), s); err != nil || ok {
		t.Fatalf("after Stop: %v, %v", ok, err)
	}
}
