package runtime

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tsexec "github.com/wuyuz99/taskspace/internal/exec"
)

const adapterState = "exists\n_root\tlive\tclaude\nbackend\tdead\t\n"

func TestAdapterStartCreatesOnlyMissingWindows(t *testing.T) {
	f := tsexec.NewFake().RespondOut("tspace-mux-wez state", adapterState)
	a := NewAdapter("wez", nil, f)
	a.Shell = "/bin/zsh"
	if err := a.Start(ctx(), "tspace@t", []Window{{Name: "_root"}, {Name: "backend"}, {Name: "web", Dir: "/w"}}); err != nil {
		t.Fatal(err)
	}
	cmds := strings.Join(f.Commands(), "\n")
	if strings.Contains(cmds, "new-session") || strings.Count(cmds, "new-window") != 1 {
		t.Fatalf("want one new-window, got:\n%s", cmds)
	}
	if !strings.Contains(cmds, "tspace-mux-wez new-window tspace@t web /w keep -- /bin/zsh") {
		t.Errorf("window arguments wrong:\n%s", cmds)
	}
}

func TestAdapterStartNewSession(t *testing.T) {
	f := tsexec.NewFake().RespondOut("state", "missing\n")
	a := NewAdapter("wez", []string{"/opt/wez-adapter", "--x"}, f)
	err := a.Start(ctx(), "tspace@t", []Window{
		{Name: "_root", Command: []string{"claude", "--model", "opus"}},
		{Name: "a.b", Dir: "/d", Command: []string{"sh"}, Transient: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	cmds := strings.Join(f.Commands(), "\n")
	if !strings.Contains(cmds, "/opt/wez-adapter --x new-session tspace@t _root  keep -- claude --model opus") ||
		!strings.Contains(cmds, "new-window tspace@t a~2eb /d close -- sh") {
		t.Errorf("got:\n%s", cmds)
	}
}

// The safety checks live in tspace, not in the adapter.
func TestAdapterEnforcesSafetyRules(t *testing.T) {
	f := tsexec.NewFake().RespondOut("state", adapterState)
	a := NewAdapter("wez", nil, f)
	if err := a.OpenWindow(ctx(), "tspace@t", Window{Name: "_root"}); err == nil {
		t.Error("duplicate window opened")
	}
	if err := a.Restart(ctx(), "tspace@t", Window{Name: "_root"}); err == nil {
		t.Error("live window restarted")
	}
	if f.Ran("new-window") || f.Ran("respawn") {
		t.Errorf("adapter asked to act: %v", f.Commands())
	}
	if err := a.Restart(ctx(), "tspace@t", Window{Name: "backend", Command: []string{"codex"}}); err != nil {
		t.Fatal(err)
	}
	if !f.Ran("respawn tspace@t backend  keep -- codex") {
		t.Errorf("no respawn: %v", f.Commands())
	}
}

func TestAdapterState(t *testing.T) {
	f := tsexec.NewFake().RespondOut("state", "exists\r\nweb\tdead\r\nweb\tlive\tvim\r\n")
	st, err := NewAdapter("wez", nil, f).State(ctx(), "tspace@t")
	if err != nil {
		t.Fatal(err)
	}
	if p := st.Panes["web"]; !st.Exists || p.Dead || p.Command != "vim" {
		t.Errorf("state = %+v; a window with any live pane is live", st)
	}
	for _, bad := range []string{"", "yes\n", "exists\nweb\n", "exists\nweb\trunning\n"} {
		f := tsexec.NewFake().RespondOut("state", bad)
		if _, err := NewAdapter("wez", nil, f).State(ctx(), "tspace@t"); err == nil {
			t.Errorf("state %q accepted", bad)
		}
	}
}

func TestAdapterFocus(t *testing.T) {
	f := tsexec.NewFake().RespondOut("focus", "yes\n")
	ok, err := NewAdapter("wez", nil, f).Focus(ctx(), "tspace@t", "backend")
	if err != nil || !ok {
		t.Errorf("Focus = %v, %v", ok, err)
	}
}

func TestAdapterCheckPassesProtocol(t *testing.T) {
	f := tsexec.NewFake()
	if err := NewAdapter("wez", nil, f).Available(ctx()); err != nil {
		t.Fatal(err)
	}
	if !f.Ran("tspace-mux-wez check " + AdapterProtocol) {
		t.Errorf("got %v", f.Commands())
	}
}

func TestNewPicksBuiltinOrAdapter(t *testing.T) {
	f := tsexec.NewFake()
	cases := []struct {
		name    string
		adapter []string
		want    string
	}{
		{"", nil, "tmux"},
		{"tmux", nil, "tmux"},
		{"zellij", nil, "zellij"},
		{"zellij", []string{"my-zellij"}, "adapter"},
		{"wezterm", nil, "adapter"},
	}
	for _, c := range cases {
		rt, err := New(c.name, c.adapter, f)
		if err != nil {
			t.Fatalf("New(%q): %v", c.name, err)
		}
		got := "?"
		switch rt.(type) {
		case *Tmux:
			got = "tmux"
		case *Zellij:
			got = "zellij"
		case *Adapter:
			got = "adapter"
		}
		if got != c.want {
			t.Errorf("New(%q, %v) = %s, want %s", c.name, c.adapter, got, c.want)
		}
	}
	for _, bad := range []string{"Tmux", "-x", "a/b", "a b"} {
		if _, err := New(bad, nil, f); err == nil {
			t.Errorf("New(%q) accepted", bad)
		}
	}
}

func TestRenameWindowCommands(t *testing.T) {
	f := tsexec.NewFake()
	if err := NewTmux(f).RenameWindow(ctx(), "tspace@t", "_git", "_lg"); err != nil {
		t.Fatal(err)
	}
	if !f.Ran("rename-window -t =tspace@t:=_git _lg") {
		t.Errorf("tmux: %v", f.Commands())
	}
	g := tsexec.NewFake()
	if err := NewAdapter("wez", nil, g).RenameWindow(ctx(), "tspace@t", "_git", "_lg"); err != nil {
		t.Fatal(err)
	}
	if !g.Ran("tspace-mux-wez rename tspace@t _git _lg") {
		t.Errorf("adapter: %v", g.Commands())
	}
}

// Only a missing program means no session can be running; an adapter that
// runs and fails says nothing about its sessions.
func TestNotInstalled(t *testing.T) {
	missing := NewAdapter("nosuch", []string{"tspace-mux-surely-not-installed"}, tsexec.Real())
	if _, err := missing.HasSession(context.Background(), "s"); !NotInstalled(err) {
		t.Fatalf("missing adapter: NotInstalled(%v) = false", err)
	}
	if err := missing.Available(context.Background()); !NotInstalled(err) {
		t.Fatalf("missing adapter: NotInstalled(%v) = false", err)
	}
	gone := NewAdapter("gone", []string{filepath.Join(t.TempDir(), "tspace-mux-gone")}, tsexec.Real())
	if _, err := gone.HasSession(context.Background(), "s"); !NotInstalled(err) {
		t.Fatalf("adapter path gone: NotInstalled(%v) = false", err)
	}
	failing := NewAdapter("failing", []string{"x"}, tsexec.NewFake().Fail("x", "broken", 23))
	if _, err := failing.HasSession(context.Background(), "s"); err == nil || NotInstalled(err) {
		t.Fatalf("failing adapter: err %v", err)
	}
}

// BELL and OUTPUT are optional, so adapters written before them still work.
func TestAdapterStateBellAndOutput(t *testing.T) {
	f := tsexec.NewFake().RespondOut("state", "exists\nweb\tlive\tvim\tbell\t1700000000\nold\tlive\tcodex\napi\tlive\tclaude\t\t\n")
	st, err := NewAdapter("wez", nil, f).State(ctx(), "tspace@t")
	if err != nil {
		t.Fatal(err)
	}
	if p := st.Panes["web"]; !p.Bell || !p.Output.Equal(time.Unix(1700000000, 0)) || p.Command != "vim" {
		t.Errorf("web = %+v", p)
	}
	for _, name := range []string{"old", "api"} {
		if p := st.Panes[name]; p.Bell || !p.Output.IsZero() || p.Dead {
			t.Errorf("%s = %+v, want live with neither", name, p)
		}
	}
}
