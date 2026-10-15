package runtime

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	tsexec "github.com/wuyuz99/taskspace/internal/exec"
)

// The reference adapter in docs/ is documentation that must keep working, so
// it runs the protocol against a private tmux server.
func TestReferenceAdapterAgainstRealTmux(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh unavailable")
	}
	script, err := filepath.Abs(filepath.Join("..", "..", "docs", "tspace-mux-tmux.sh"))
	if err != nil {
		t.Fatal(err)
	}
	// Socket length is limited on Unix; Go's per-test directory can be long.
	dir, err := os.MkdirTemp("", "tspace-ad-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	t.Setenv("TMUX_TMPDIR", dir)
	t.Setenv("TMUX", "")
	t.Cleanup(func() { exec.Command("tmux", "kill-server").Run() })

	a := NewAdapter("reftmux", []string{sh, script}, tsexec.Real())
	if err := a.Available(ctx()); err != nil {
		t.Fatal(err)
	}
	s := a.SessionName("a.b")
	windows := []Window{
		{Name: "_root", Dir: dir, Command: []string{"sleep", "60"}},
		{Name: "api.v1", Dir: dir, Command: []string{"false"}},
	}
	if err := a.Start(ctx(), s, windows); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		st, err := a.State(ctx(), s)
		if err != nil {
			t.Fatal(err)
		}
		if p, ok := st.Panes[WindowName("api.v1")]; ok && p.Dead {
			if root := st.Panes["_root"]; root.Dead || root.Command != "sleep" {
				t.Fatalf("_root = %+v", root)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("exited command not kept: %+v", st)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := a.Start(ctx(), s, windows); err != nil {
		t.Fatalf("second Start: %v", err)
	}
	if err := a.Restart(ctx(), s, windows[0]); err == nil {
		t.Fatal("live window restarted")
	}
	if err := a.Restart(ctx(), s, Window{Name: "api.v1", Dir: dir, Command: []string{"sleep", "60"}}); err != nil {
		t.Fatal(err)
	}
	if err := a.OpenWindow(ctx(), s, Window{Name: "_x", Dir: dir, Command: []string{"sh", "-c", "exit 0"}, Transient: true}); err != nil {
		t.Fatal(err)
	}
	st, err := a.State(ctx(), s)
	if err != nil {
		t.Fatal(err)
	}
	if st.Panes[WindowName("api.v1")].Dead {
		t.Fatalf("restarted window still dead: %+v", st)
	}
	if err := a.Stop(ctx(), s); err != nil {
		t.Fatal(err)
	}
	if ok, err := a.HasSession(ctx(), s); err != nil || ok {
		t.Fatalf("after Stop: %v, %v", ok, err)
	}
}
