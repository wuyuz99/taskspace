package cli

import (
	"context"
	"os"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"strings"
	"testing"

	"github.com/aw-tool/aw/internal/runtime"
	"github.com/aw-tool/aw/internal/task"
)

type restartRuntime struct {
	stubRuntime
	restarted []runtime.Window
}

func (r *restartRuntime) Restart(_ context.Context, _ string, w runtime.Window) error {
	r.restarted = append(r.restarted, w)
	return nil
}

func (h *harness) agentOf(t *testing.T, name string) []string {
	t.Helper()
	tk, err := h.app.Store.Load(name)
	if err != nil {
		t.Fatal(err)
	}
	return tk.Agent
}

func TestTaskAgentFlags(t *testing.T) {
	h := newHarness(t)
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-142", "--agent", "claude", "--agent=--model", "--agent", "opus"); err != nil {
		t.Fatal(err)
	}
	if got := h.agentOf(t, "PAY-142"); !slices.Equal(got, []string{"claude", "--model", "opus"}) {
		t.Fatalf("agent: %q", got)
	}
	if err := h.exec(t, h.app.newTaskCmd(), "show", "PAY-142"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.out.String(), "agent:     claude --model opus (window _root") {
		t.Fatalf("show: %s", h.out.String())
	}

	for _, bad := range [][]string{
		{"update", "PAY-142", "--agent", "codex", "--shell"},
		{"update", "PAY-142", "--agent", " "},
	} {
		if err := h.exec(t, h.app.newTaskCmd(), bad...); err == nil {
			t.Fatalf("%v accepted", bad)
		}
		if got := h.agentOf(t, "PAY-142"); !slices.Equal(got, []string{"claude", "--model", "opus"}) {
			t.Fatalf("%v changed the agent: %q", bad, got)
		}
	}

	if err := h.exec(t, h.app.newTaskCmd(), "update", "PAY-142", "--shell"); err != nil {
		t.Fatal(err)
	}
	if got := h.agentOf(t, "PAY-142"); got != nil {
		t.Fatalf("--shell left %q", got)
	}
	if err := h.exec(t, h.app.newTaskCmd(), "update", "PAY-142", "--agent", "codex"); err != nil {
		t.Fatal(err)
	}
	if got := h.agentOf(t, "PAY-142"); !slices.Equal(got, []string{"codex"}) {
		t.Fatalf("agent: %q", got)
	}
	if !strings.Contains(h.out.String(), "agent: codex") {
		t.Fatalf("update output: %s", h.out.String())
	}

	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-143", "--agent", ""); err == nil {
		t.Fatal("empty agent accepted")
	}
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-143", "--agent", "claude", "--shell"); err == nil {
		t.Fatal("--agent and --shell accepted")
	}
	if h.app.Store.Exists("PAY-143") {
		t.Fatal("rejected create left a record")
	}
}

func TestStartOpensRootWindowFirst(t *testing.T) {
	h := newHarness(t)
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-142", "--agent", "claude",
		"--repo", "backend="+h.repo, "--repo", "frontend="+mkRepo(t)); err != nil {
		t.Fatal(err)
	}
	rt := &openRuntime{}
	h.app.Runtime = rt

	if err := h.exec(t, h.app.newStartCmd(), "PAY-142"); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, w := range rt.windows {
		names = append(names, w.Name)
	}
	if strings.Join(names, ",") != "_root,backend,frontend" {
		t.Fatalf("windows: %v", names)
	}
	root := rt.windows[0]
	if root.Dir != h.app.Store.TaskDir("PAY-142") || !slices.Equal(root.Command, []string{"claude"}) {
		t.Fatalf("root window: %+v", root)
	}

	if err := h.exec(t, h.app.newStartCmd(), "PAY-142", "backend"); err != nil {
		t.Fatal(err)
	}
	if len(rt.windows) != 1 || rt.windows[0].Name != "backend" {
		t.Fatalf("naming a repository still started others: %+v", rt.windows)
	}

	// Without an agent the root window is still there, running a shell.
	if err := h.exec(t, h.app.newTaskCmd(), "update", "PAY-142", "--shell"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newStartCmd(), "PAY-142"); err != nil {
		t.Fatal(err)
	}
	if rt.windows[0].Name != task.RootWindowName || rt.windows[0].Command != nil {
		t.Fatalf("shell root window: %+v", rt.windows[0])
	}
}

func TestOpenPointsAtRootRestart(t *testing.T) {
	h := newHarness(t)
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-142", "--agent", "claude", "--repo", "backend="+h.repo); err != nil {
		t.Fatal(err)
	}
	h.app.Runtime = &openRuntime{}
	if err := h.exec(t, h.app.newOpenCmd(), "PAY-142"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.errb.String(), "aw restart PAY-142 --root") {
		t.Fatalf("root restart hint missing: %s", h.errb.String())
	}
}

func TestRestartRoot(t *testing.T) {
	h := newHarness(t)
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-142", "--agent", "claude", "--repo", "backend="+h.repo); err != nil {
		t.Fatal(err)
	}
	rt := &restartRuntime{}
	h.app.Runtime = rt

	if err := h.exec(t, h.app.newRestartCmd(), "PAY-142", "--root"); err == nil {
		t.Fatal("restarted a root window before its directory existed")
	}
	if err := h.exec(t, h.app.newStartCmd(), "PAY-142", "--no-runtime"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]string{{"PAY-142"}, {"PAY-142", "backend", "--root"}} {
		if err := h.exec(t, h.app.newRestartCmd(), bad...); err == nil {
			t.Fatalf("%v accepted", bad)
		}
	}
	if len(rt.restarted) != 0 {
		t.Fatalf("rejected restarts reached the runtime: %+v", rt.restarted)
	}

	if err := h.exec(t, h.app.newRestartCmd(), "PAY-142", "--root"); err != nil {
		t.Fatal(err)
	}
	want := runtime.Window{Name: task.RootWindowName, Dir: h.app.Store.TaskDir("PAY-142"), Command: []string{"claude"}}
	if len(rt.restarted) != 1 || rt.restarted[0].Name != want.Name || rt.restarted[0].Dir != want.Dir || !slices.Equal(rt.restarted[0].Command, want.Command) {
		t.Fatalf("restarted: %+v", rt.restarted)
	}
	if err := h.exec(t, h.app.newRestartCmd(), "PAY-142", "backend"); err != nil {
		t.Fatal(err)
	}
	if got := rt.restarted[1]; got.Name != "backend" || got.Dir != h.app.Store.WorktreePath("PAY-142", "backend") {
		t.Fatalf("repository restart: %+v", got)
	}
}

func TestPathWithoutRepoDefaultsToRootUnlessOneRepo(t *testing.T) {
	h := newHarness(t)
	tk, err := h.app.Store.Create("PAY-142")
	if err != nil {
		t.Fatal(err)
	}
	root := h.app.Store.TaskDir("PAY-142")
	path := func() string {
		t.Helper()
		if err := h.exec(t, h.app.newPathCmd(), "PAY-142"); err != nil {
			t.Fatal(err)
		}
		return strings.TrimSuffix(h.out.String(), "\n")
	}

	if got := path(); got != root {
		t.Fatalf("no repositories: %q", got)
	}
	tk.SetRepo("backend", task.Repo{Worktree: h.app.Store.WorktreePath("PAY-142", "backend")})
	if err := h.app.Store.Save(tk); err != nil {
		t.Fatal(err)
	}
	if got := path(); got != h.app.Store.WorktreePath("PAY-142", "backend") {
		t.Fatalf("one repository: %q", got)
	}
	tk.SetRepo("frontend", task.Repo{})
	if err := h.app.Store.Save(tk); err != nil {
		t.Fatal(err)
	}
	if got := path(); got != root {
		t.Fatalf("several repositories: %q", got)
	}
}

func TestShellWithoutRepoOpensRootOfMultiRepoTask(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("Unix shell test")
	}
	h := newHarness(t)
	tk, err := h.app.Store.Create("PAY-142")
	if err != nil {
		t.Fatal(err)
	}
	tk.SetRepo("backend", task.Repo{})
	tk.SetRepo("frontend", task.Repo{})
	if err := h.app.Store.Save(tk); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(t.TempDir(), "probe")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s\\n' \"$PWD\" \"$AW_REPO\" > probe.txt\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", script)
	if err := h.exec(t, h.app.newShellCmd(), "PAY-142"); err != nil {
		t.Fatal(err)
	}
	dir := h.app.Store.TaskDir("PAY-142")
	b, err := os.ReadFile(filepath.Join(dir, "probe.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != dir+"\n\n" {
		t.Fatalf("shell: %q", b)
	}
}

func TestProjectRecipeCarriesRootAgent(t *testing.T) {
	h := newHarness(t)
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-142", "--agent", "claude", "--repo", "backend="+h.repo); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newProjectCmd(), "save", "payments", "--from", "PAY-142"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newProjectCmd(), "show", "payments"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.out.String(), "agent:   claude (task root)") {
		t.Fatalf("show: %s", h.out.String())
	}

	for name, flags := range map[string][]string{
		"PAY-150": nil,
		"PAY-151": {"--shell"},
		"PAY-152": {"--agent", "codex"},
	} {
		args := append([]string{"create", name, "--project", "payments"}, flags...)
		if err := h.exec(t, h.app.newTaskCmd(), args...); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if got := h.agentOf(t, "PAY-150"); !slices.Equal(got, []string{"claude"}) {
		t.Fatalf("recipe agent: %q", got)
	}
	if got := h.agentOf(t, "PAY-151"); got != nil {
		t.Fatalf("--shell over recipe: %q", got)
	}
	if got := h.agentOf(t, "PAY-152"); !slices.Equal(got, []string{"codex"}) {
		t.Fatalf("--agent over recipe: %q", got)
	}
}

func TestStatusLeadsWithRootRow(t *testing.T) {
	h := newHarness(t)
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-142", "--agent", "claude", "--repo", "backend="+h.repo); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newStatusCmd(), "PAY-142"); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(h.out.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("status: %s", h.out.String())
	}
	if f := strings.Fields(lines[1]); strings.Join(f, " ") != "PAY-142 (root) - claude no-session -" {
		t.Fatalf("root row: %q", lines[1])
	}
	if !strings.HasPrefix(strings.Join(strings.Fields(lines[2]), " "), "PAY-142 backend agent/PAY-142 shell no-session") {
		t.Fatalf("repo row: %q", lines[2])
	}
}
