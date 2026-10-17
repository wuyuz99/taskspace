package runtime

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tsexec "github.com/wuyuz99/taskspace/internal/exec"
)

func TestNamesDoNotCollide(t *testing.T) {
	seen := map[string]bool{}
	for _, name := range []string{"a.b", "a-b", "a~2eb", "a_b", "1", "v2-a", "a:b"} {
		encoded := WindowName(name)
		if seen[encoded] {
			t.Fatalf("collision for %s", name)
		}
		seen[encoded] = true
		if strings.ContainsAny(encoded, ".:") {
			t.Fatalf("target syntax in %s", encoded)
		}
	}
}

func TestRestartRefusesLiveOrMissingWindow(t *testing.T) {
	for _, output := range []string{"backend\t0\tsh\n", "", "backend\t1\tfalse\nbackend\t0\tsh\n"} {
		f := tsexec.NewFake().RespondOut("list-panes", output)
		if err := NewTmux(f).Restart(ctx(), "tspace-task", Window{Name: "backend"}); err == nil {
			t.Fatal("expected refusal")
		}
		if f.Ran("respawn-window") {
			t.Fatal("refusal changed window")
		}
	}
	f := tsexec.NewFake().Fail("has-session", "missing", 1)
	if err := NewTmux(f).Restart(ctx(), "tspace-task", Window{Name: "backend"}); err == nil {
		t.Fatal("missing session accepted")
	}
}

func TestRestartDeadWindowDoesNotForce(t *testing.T) {
	f := tsexec.NewFake().RespondOut("list-panes", "backend\t1\tfalse\n")
	if err := NewTmux(f).Restart(ctx(), "tspace-task", Window{Name: "backend", Dir: "/work", Command: []string{"sleep", "10"}}); err != nil {
		t.Fatal(err)
	}
	if !f.Ran("respawn-window -t =tspace-task:=backend -c /work sleep 10") || f.Ran("respawn-window -k") {
		t.Fatalf("unsafe argv: %v", f.Commands())
	}
}

func TestLegacySessionBlocksStart(t *testing.T) {
	f := tsexec.NewFake() // has-session succeeds for the legacy name
	tm := NewTmux(f)
	if err := tm.Start(ctx(), tm.SessionName("a.b"), []Window{{Name: "repo"}}); err == nil || !strings.Contains(err.Error(), "legacy") {
		t.Fatalf("error: %v", err)
	}
	if f.Ran("new-session") || f.Ran("kill-session") {
		t.Fatal("legacy session mutated")
	}
}

func TestLegacySessionNames(t *testing.T) {
	got := legacySessions(NewTmux(tsexec.NewFake()).SessionName("a.b"))
	if want := []string{"aw@a~2eb", "aw-a-b"}; strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("legacy names = %v, want %v", got, want)
	}
}

// isolatedRunner supplies a private tmux socket without touching the user's server.
type isolatedRunner struct{ socket string }

func (r isolatedRunner) Run(ctx context.Context, dir string, argv ...string) (tsexec.Result, error) {
	if argv[0] == "tmux" {
		argv = append([]string{"tmux", "-S", r.socket}, argv[1:]...)
	}
	return tsexec.Real().Run(ctx, dir, argv...)
}

func TestRealTmuxIsolationAndRecovery(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}
	// Socket length is limited on Unix; Go's per-test directory can be long.
	dir, err := os.MkdirTemp("", "tspace-rt-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	r := isolatedRunner{filepath.Join(dir, "socket")}
	tm := NewTmux(r)
	tm.StopGrace = 0
	t.Cleanup(func() { r.Run(ctx(), "", "tmux", "kill-server") })
	s1, s2 := tm.SessionName("a.b"), tm.SessionName("a-b")
	windows := []Window{{Name: "api.v1", Dir: dir, Command: []string{"false"}}, {Name: "api-v1", Dir: dir, Command: []string{"sleep", "60"}}}
	if err := tm.Start(ctx(), s1, windows); err != nil {
		t.Fatal(err)
	}
	if err := tm.Start(ctx(), s2, []Window{{Name: "repo", Dir: dir, Command: []string{"sleep", "60"}}}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		st, err := tm.State(ctx(), s1)
		if err != nil {
			t.Fatal(err)
		}
		if p, ok := st.Panes[WindowName("api.v1")]; ok && p.Dead {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("immediately exiting command was not retained")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := tm.Start(ctx(), s1, windows); err != nil {
		t.Fatal(err)
	}
	if err := tm.Restart(ctx(), s1, windows[1]); err == nil {
		t.Fatal("live window restarted")
	}
	if err := tm.Restart(ctx(), s1, Window{Name: "api.v1", Dir: dir, Command: []string{"sleep", "60"}}); err != nil {
		t.Fatal(err)
	}
	st, err := tm.State(ctx(), s1)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Panes) != 2 || st.Panes[WindowName("api.v1")].Dead {
		t.Fatalf("unexpected state: %+v", st)
	}
	if err := tm.Stop(ctx(), s1); err != nil {
		t.Fatal(err)
	}
	if exists, err := tm.HasSession(ctx(), s2); err != nil || !exists {
		t.Fatalf("stopping dotted task affected hyphenated task: %v", err)
	}
}

// Opening a window must not make it the active one: an attached client, or
// the next attach, would land on it.
func TestRealTmuxNewWindowsStayInTheBackground(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}
	dir, err := os.MkdirTemp("", "tspace-rt-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	r := isolatedRunner{filepath.Join(dir, "socket")}
	tm := NewTmux(r)
	t.Cleanup(func() { r.Run(ctx(), "", "tmux", "kill-server") })
	s := tm.SessionName("bg")
	if err := tm.Start(ctx(), s, []Window{{Name: "_root", Dir: dir, Command: []string{"sleep", "60"}}, {Name: "backend", Dir: dir}}); err != nil {
		t.Fatal(err)
	}
	if err := tm.OpenWindow(ctx(), s, Window{Name: "_main-shell", Dir: dir, Transient: true}); err != nil {
		t.Fatal(err)
	}
	res, err := r.Run(ctx(), "", "tmux", "list-windows", "-t", target(s), "-F", "#{window_active} #{window_name}")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Stdout, "1 _root\n") {
		t.Fatalf("_root is not the active window:\n%s", res.Stdout)
	}
}
