package task

import (
	"os"
	"strings"
	"testing"
)

func TestConfigBranchPrefix(t *testing.T) {
	var c Config
	if got := c.Branch("t"); got != "agent/t" {
		t.Errorf("default Branch = %q, want agent/t", got)
	}
	custom := "isaac/"
	c.BranchPrefix = &custom
	if got := c.Branch("t"); got != "isaac/t" {
		t.Errorf("custom Branch = %q, want isaac/t", got)
	}
	// An empty prefix is a real choice: the branch is the task name alone.
	empty := ""
	c.BranchPrefix = &empty
	if got := c.Branch("t"); got != "t" {
		t.Errorf("empty-prefix Branch = %q, want t", got)
	}
}

func TestConfigRepoNamesAreSorted(t *testing.T) {
	c := Config{Repos: map[string]string{"infra": "/i", "backend": "/b", "frontend": "/f"}}
	if got := strings.Join(c.RepoNames(), ","); got != "backend,frontend,infra" {
		t.Errorf("RepoNames = %s", got)
	}
	if got := (&Config{}).RepoNames(); len(got) != 0 {
		t.Errorf("empty RepoNames = %v", got)
	}
}

func TestConfigRoundTrip(t *testing.T) {
	s := newTestStore(t)

	// A missing file is an empty config, not an error.
	c, err := s.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig on a fresh root: %v", err)
	}
	if c.Multiplexer != "" || c.BranchPrefix != nil || len(c.Repos) != 0 {
		t.Fatalf("fresh config = %+v, want empty", c)
	}

	prefix := ""
	want := &Config{
		Agent:        []string{"claude"},
		Repos:        map[string]string{"backend": "/src/backend"},
		BranchPrefix: &prefix,
		Multiplexer:  "zellij",
		Adapters:     map[string][]string{"wezterm": {"wez-adapter", "--json"}},
	}
	if err := s.SaveConfig(want); err != nil {
		t.Fatal(err)
	}
	got, err := s.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got.Multiplexer != "zellij" || got.Repos["backend"] != "/src/backend" ||
		len(got.Agent) != 1 || len(got.Adapters["wezterm"]) != 2 {
		t.Errorf("round trip = %+v", got)
	}
	// The pointer keeps "set to empty" distinct from "never set".
	if got.BranchPrefix == nil || *got.BranchPrefix != "" {
		t.Errorf("BranchPrefix = %v, want pointer to empty", got.BranchPrefix)
	}
}

func TestLoadConfigErrors(t *testing.T) {
	s := newTestStore(t)
	mustWrite(t, s.ConfigPath(), "{not json")
	if _, err := s.LoadConfig(); err == nil || !strings.Contains(err.Error(), "parse config") {
		t.Fatalf("malformed config error = %v", err)
	}

	// A directory where the file should be cannot be read.
	s = newTestStore(t)
	if err := os.Mkdir(s.ConfigPath(), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LoadConfig(); err == nil || !strings.Contains(err.Error(), "read config") {
		t.Fatalf("unreadable config error = %v", err)
	}
	// Nor replaced by a file.
	if err := s.SaveConfig(&Config{}); err == nil {
		t.Fatal("SaveConfig replaced a directory")
	}
}
