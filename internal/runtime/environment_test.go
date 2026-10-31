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
	"github.com/wuyuz99/taskspace/internal/git"
)

// Run a real child using the context passed to each backend's runner.
type environmentProbe struct{ t *testing.T }

func (p environmentProbe) Run(ctx context.Context, _ string, _ ...string) (tsexec.Result, error) {
	res, err := tsexec.Real().Run(ctx, "", "env")
	if err != nil {
		p.t.Fatal(err)
	}
	for _, key := range git.RepositoryEnvVars() {
		if strings.Contains("\n"+res.Stdout, "\n"+key+"=") {
			p.t.Errorf("runtime child inherited %s", key)
		}
	}
	if !strings.Contains(res.Stdout, "GIT_SSH_COMMAND=ssh custom") {
		p.t.Error("lost SSH configuration")
	}
	return tsexec.Result{}, nil
}

func TestRuntimeProcessesClearRepositoryEnvironment(t *testing.T) {
	for _, key := range git.RepositoryEnvVars() {
		t.Setenv(key, "poison")
	}
	t.Setenv("GIT_SSH_COMMAND", "ssh custom")
	r := environmentProbe{t}
	for _, rt := range []Runtime{NewTmux(r), NewZellij(r), NewAdapter("probe", nil, r)} {
		if err := rt.Available(ctx()); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("ZELLIJ", "0")
	t.Setenv("ZELLIJ_SESSION_NAME", "parent")
	if _, err := NewZellij(r).detached(ctx(), "--version"); err != nil {
		t.Fatal(err)
	}
}

func cleanLaunchPrefix() string {
	argv := []string{"env"}
	for _, key := range git.RepositoryEnvVars() {
		argv = append(argv, "-u", key)
	}
	return strings.Join(argv, " ")
}

func TestLaunchClearsServerRepositoryEnvironment(t *testing.T) {
	for _, key := range git.RepositoryEnvVars() {
		t.Setenv(key, "poison")
	}
	t.Setenv("GIT_SSH_COMMAND", "ssh custom")
	for _, shell := range []bool{false, true} {
		w := Window{Command: []string{"env"}, Env: []string{"TSPACE_TASK=demo", "GIT_DIR=also-poison"}}
		if shell {
			w.Command = nil
		}
		argv := launchArgv(w, "env")
		out, err := exec.Command(argv[0], argv[1:]...).Output()
		if err != nil {
			t.Fatal(err)
		}
		for _, key := range git.RepositoryEnvVars() {
			if strings.Contains("\n"+string(out), "\n"+key+"=") {
				t.Fatalf("inherited %s: %s", key, out)
			}
		}
		if !strings.Contains(string(out), "GIT_SSH_COMMAND=ssh custom") || !strings.Contains(string(out), "TSPACE_TASK=demo") {
			t.Fatalf("lost user/window environment: %s", out)
		}
	}
}

func TestTmuxRepositoryEnvironment(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}
	dir, err := os.MkdirTemp("", "tspace-env-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	t.Setenv("TMUX_TMPDIR", dir)
	t.Setenv("TMUX", "")
	t.Cleanup(func() { exec.Command("tmux", "kill-server").Run() })
	repo := filepath.Join(dir, "repo")
	if out, err := exec.Command("git", "init", repo).CombinedOutput(); err != nil {
		t.Fatalf("init: %s: %v", out, err)
	}
	t.Setenv("GIT_DIR", "/wrong/repo")
	t.Setenv("GIT_WORK_TREE", "/wrong/tree")
	tm := NewTmux(nil)
	check := func(session, name string, start func(Window) error) {
		t.Helper()
		result := filepath.Join(dir, name)
		w := Window{Name: name, Dir: repo, Command: []string{"sh", "-c", `git rev-parse --show-toplevel > "$1"`, "probe", result}}
		if err := start(w); err != nil {
			t.Fatal(err)
		}
		for deadline := time.Now().Add(5 * time.Second); ; {
			st, err := tm.State(ctx(), session)
			if err != nil {
				t.Fatal(err)
			}
			if p, ok := st.Panes[name]; ok && p.Dead {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("pane did not finish")
			}
			time.Sleep(20 * time.Millisecond)
		}
		b, err := os.ReadFile(result)
		if err != nil || strings.TrimSpace(string(b)) != repo {
			t.Fatalf("git ran in %q, want %q: %v", b, repo, err)
		}
	}
	s := tm.SessionName("env")
	check(s, "first", func(w Window) error { return tm.Start(ctx(), s, []Window{w}) })
	for _, key := range []string{"GIT_DIR", "GIT_WORK_TREE"} {
		if out, err := exec.Command("tmux", "show-environment", "-g", key).CombinedOutput(); err == nil {
			t.Fatalf("new server retained %s: %s", key, out)
		}
		if out, err := exec.Command("tmux", "set-environment", "-g", key, "/old/poison").CombinedOutput(); err != nil {
			t.Fatalf("poison server: %s: %v", out, err)
		}
		t.Setenv(key, "")
		os.Unsetenv(key)
	}
	check(s, "added", func(w Window) error { return tm.OpenWindow(ctx(), s, w) })
	check(s, "added", func(w Window) error { return tm.Restart(ctx(), s, w) })
	second := tm.SessionName("later")
	check(second, "later", func(w Window) error { return tm.Start(ctx(), second, []Window{w}) })
}
