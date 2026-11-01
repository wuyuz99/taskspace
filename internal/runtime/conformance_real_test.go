package runtime_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync/atomic"
	"testing"

	tsexec "github.com/wuyuz99/taskspace/internal/exec"
	"github.com/wuyuz99/taskspace/internal/runtime"
	"github.com/wuyuz99/taskspace/internal/runtime/runtimetest"
)

// The same cases as runtimetest's fake, on a private tmux server.
func TestRealTmuxConformance(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}
	// Socket length is limited on Unix; Go's per-test directory can be long.
	dir, err := os.MkdirTemp("", "tspace-conf-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	t.Setenv("TMUX_TMPDIR", dir)
	t.Setenv("TMUX", "")
	t.Cleanup(func() { exec.Command("tmux", "kill-server").Run() })

	var n atomic.Int32
	runtimetest.Conformance(t, func(*testing.T) runtimetest.Target {
		tm := runtime.NewTmux(tsexec.Real())
		tm.StopGrace = 0
		return runtimetest.Target{
			Runtime: tm,
			Session: func(*testing.T) string { return fmt.Sprintf("tspace@conf-%d", n.Add(1)) },
		}
	})
}

// zellij has no private-socket option, so sessions get names no person would
// use, and it needs a client to add a tab.
func TestRealZellijConformance(t *testing.T) {
	if _, err := exec.LookPath("zellij"); err != nil {
		t.Skip("zellij unavailable")
	}
	z := runtime.NewZellij(tsexec.Real())
	z.StopGrace = 0
	if err := z.Available(context.Background()); err != nil {
		t.Skip(err)
	}
	t.Setenv("ZELLIJ", "")

	var n atomic.Int32
	runtimetest.Conformance(t, func(*testing.T) runtimetest.Target {
		return runtimetest.Target{
			Runtime:     z,
			NeedsClient: true,
			// Not checked on zellij: tspace relies on it only for tmux.
			KeepsEmptySession: true,
			Session: func(t *testing.T) string {
				s := fmt.Sprintf("tspace@zc-%d-%d", os.Getpid(), n.Add(1))
				t.Cleanup(func() {
					exec.Command("zellij", "kill-session", s).Run()
					exec.Command("zellij", "delete-session", s).Run()
				})
				return s
			},
		}
	})
}
