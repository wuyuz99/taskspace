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
				t.Fatalf("%s: %+v", what, st)
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
	if err := z.Stop(ctx(), s); err != nil {
		t.Fatal(err)
	}
	if ok, err := z.HasSession(ctx(), s); err != nil || ok {
		t.Fatalf("after Stop: %v, %v", ok, err)
	}
}
