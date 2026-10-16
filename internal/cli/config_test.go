package cli

import (
	"os"
	"slices"
	"strings"
	"testing"
)

func TestConfigAgentPassesFlagsThrough(t *testing.T) {
	h := newHarness(t)
	if err := h.exec(t, h.app.newConfigCmd(), "agent"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.out.String(), "none") {
		t.Fatalf("unset default: %q", h.out.String())
	}

	if err := h.exec(t, h.app.newConfigCmd(), "agent", "claude", "--model", "opus"); err != nil {
		t.Fatal(err)
	}
	c, err := h.app.Store.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(c.Agent, []string{"claude", "--model", "opus"}) {
		t.Fatalf("agent: %q", c.Agent)
	}
	if err := h.exec(t, h.app.newConfigCmd()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.out.String(), "agent:  claude --model opus") {
		t.Fatalf("config: %q", h.out.String())
	}

	for _, bad := range [][]string{{"agent", "--unset", "codex"}, {"agent", " "}} {
		if err := h.exec(t, h.app.newConfigCmd(), bad...); err == nil {
			t.Fatalf("%v accepted", bad)
		}
	}
	if c, _ := h.app.Store.LoadConfig(); !slices.Equal(c.Agent, []string{"claude", "--model", "opus"}) {
		t.Fatalf("rejected input changed the default: %q", c.Agent)
	}

	if err := h.exec(t, h.app.newConfigCmd(), "agent", "--unset"); err != nil {
		t.Fatal(err)
	}
	if c, _ := h.app.Store.LoadConfig(); c.Agent != nil {
		t.Fatalf("--unset left %q", c.Agent)
	}
}

// Flag beats recipe beats default beats shell, and the winner is recorded.
func TestTaskCreateAgentPrecedence(t *testing.T) {
	h := newHarness(t)
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-100", "--repo", "backend="+h.repo); err != nil {
		t.Fatal(err)
	}
	if got := h.agentOf(t, "PAY-100"); got != nil {
		t.Fatalf("no default: %q", got)
	}

	if err := h.exec(t, h.app.newConfigCmd(), "agent", "claude"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-101"); err != nil {
		t.Fatal(err)
	}
	if got := h.agentOf(t, "PAY-101"); !slices.Equal(got, []string{"claude"}) {
		t.Fatalf("default: %q", got)
	}
	if !strings.Contains(h.out.String(), "agent: claude (task root, from tspace config)") {
		t.Fatalf("create output: %q", h.out.String())
	}

	// A recipe saved from a shell-root task names no agent, so the default applies.
	if err := h.exec(t, h.app.newProjectCmd(), "save", "plain", "--from", "PAY-100"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newTaskCmd(), "update", "PAY-100", "--agent", "codex"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newProjectCmd(), "save", "codex", "--from", "PAY-100"); err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		args []string
		want []string
	}{
		"PAY-102": {[]string{"--project", "codex"}, []string{"codex"}},
		"PAY-103": {[]string{"--project", "plain"}, []string{"claude"}},
		"PAY-104": {[]string{"--project", "codex", "--agent", "aider"}, []string{"aider"}},
		"PAY-105": {[]string{"--shell"}, nil},
	}
	for name, c := range cases {
		if err := h.exec(t, h.app.newTaskCmd(), append([]string{"create", name}, c.args...)...); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := h.agentOf(t, name); !slices.Equal(got, c.want) {
			t.Fatalf("%s %v: agent %q, want %q", name, c.args, got, c.want)
		}
	}

	// The default is a snapshot: changing it leaves existing tasks alone.
	if err := h.exec(t, h.app.newConfigCmd(), "agent", "--unset"); err != nil {
		t.Fatal(err)
	}
	if got := h.agentOf(t, "PAY-101"); !slices.Equal(got, []string{"claude"}) {
		t.Fatalf("existing task followed the config: %q", got)
	}
}

func TestBrokenConfigStopsTaskCreate(t *testing.T) {
	h := newHarness(t)
	if err := os.MkdirAll(h.app.Store.Root(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(h.app.Store.ConfigPath(), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-142"); err == nil {
		t.Fatal("created a task while the default agent could not be read")
	}
	if h.app.Store.Exists("PAY-142") {
		t.Fatal("failed create left a record")
	}
	// Naming the agent explicitly does not need the default.
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-142", "--shell"); err != nil {
		t.Fatal(err)
	}
}

func TestConfigMultiplexer(t *testing.T) {
	t.Setenv("TSPACE_MULTIPLEXER", "")
	h := newHarness(t)
	if err := h.exec(t, h.app.newConfigCmd(), "multiplexer"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.out.String(), "tmux (built in) (default)") {
		t.Fatalf("default: %q", h.out.String())
	}

	if err := h.exec(t, h.app.newConfigCmd(), "multiplexer", "zellij"); err != nil {
		t.Fatal(err)
	}
	if c, _ := h.app.Store.LoadConfig(); c.Multiplexer != "zellij" || multiplexerName(c) != "zellij" {
		t.Fatalf("zellij not chosen: %+v", c)
	}

	// An adapter's own flags are not tspace's.
	if err := h.exec(t, h.app.newConfigCmd(), "multiplexer", "wez", "/opt/wez-adapter", "--unset"); err != nil {
		t.Fatal(err)
	}
	c, _ := h.app.Store.LoadConfig()
	if c.Multiplexer != "wez" || !slices.Equal(c.Adapters["wez"], []string{"/opt/wez-adapter", "--unset"}) {
		t.Fatalf("adapter: %+v", c)
	}
	if err := h.exec(t, h.app.newConfigCmd()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.out.String(), "multiplexer: wez, through adapter /opt/wez-adapter --unset") {
		t.Fatalf("config: %q", h.out.String())
	}

	t.Setenv("TSPACE_MULTIPLEXER", "zellij")
	if name := multiplexerName(c); name != "zellij" {
		t.Fatalf("environment did not override: %s", name)
	}
	t.Setenv("TSPACE_MULTIPLEXER", "")

	for _, bad := range [][]string{{"multiplexer", "Bad/Name"}, {"multiplexer", "--unset", "tmux"}} {
		if err := h.exec(t, h.app.newConfigCmd(), bad...); err == nil {
			t.Fatalf("%v accepted", bad)
		}
	}

	if err := h.exec(t, h.app.newConfigCmd(), "multiplexer", "--unset"); err != nil {
		t.Fatal(err)
	}
	if c, _ := h.app.Store.LoadConfig(); c.Multiplexer != "" || len(c.Adapters["wez"]) == 0 {
		t.Fatalf("--unset: %+v", c)
	}

	// Choosing a name again without a command drops the command given before,
	// which for a built-in name means going back to the built-in support.
	for _, args := range [][]string{
		{"multiplexer", "zellij", "/opt/try-zellij"},
		{"multiplexer", "zellij"},
		{"multiplexer", "tmux", "/opt/try-tmux"},
		{"multiplexer", "--unset"},
	} {
		if err := h.exec(t, h.app.newConfigCmd(), args...); err != nil {
			t.Fatal(err)
		}
	}
	c, _ = h.app.Store.LoadConfig()
	if _, ok := c.Adapters["zellij"]; ok {
		t.Fatalf("zellij adapter kept after choosing zellij alone: %+v", c)
	}
	if _, ok := c.Adapters["tmux"]; ok {
		t.Fatalf("tmux adapter kept after --unset: %+v", c)
	}
	if err := h.exec(t, h.app.newConfigCmd(), "multiplexer", "wez"); err != nil {
		t.Fatal(err)
	}
	if c, _ := h.app.Store.LoadConfig(); c.Adapters != nil {
		t.Fatalf("empty adapter map kept: %+v", c)
	}
}

func TestBadMultiplexerKeepsOtherCommandsWorking(t *testing.T) {
	t.Setenv("TSPACE_MULTIPLEXER", "Not/Valid")
	h := newHarness(t)
	rt := configuredRuntime(h.app.Store, nil)
	if err := rt.Available(t.Context()); err == nil || !strings.Contains(err.Error(), "invalid multiplexer") {
		t.Fatalf("Available = %v", err)
	}
	if got := rt.SessionName("a.b"); got != "tspace@a~2eb" {
		t.Fatalf("SessionName = %s", got)
	}
}
