package cli

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wuyuz99/taskspace/internal/runtime"
	"github.com/wuyuz99/taskspace/internal/task"
)

// layoutRuntime is a session that may already be running, and records the
// windows started or opened in it.
type layoutRuntime struct {
	stubRuntime
	running bool
	opened  []runtime.Window
	openErr error
	// hasErr is what a session check fails with.
	hasErr error
}

func (r *layoutRuntime) HasSession(context.Context, string) (bool, error) {
	return r.running, r.hasErr
}
func (r *layoutRuntime) CloseWindow(_ context.Context, _ string, name string) error {
	var kept []runtime.Window
	for _, w := range r.started {
		if w.Name != name {
			kept = append(kept, w)
		}
	}
	r.started = kept
	return nil
}
func (r *layoutRuntime) State(ctx context.Context, s string) (runtime.SessionState, error) {
	st, err := r.stubRuntime.State(ctx, s)
	st.Exists = st.Exists || r.running
	return st, err
}
func (r *layoutRuntime) OpenWindow(_ context.Context, _ string, w runtime.Window) error {
	if r.openErr != nil {
		return r.openErr
	}
	r.opened = append(r.opened, w)
	return nil
}

func (rt *layoutRuntime) window(name string) (runtime.Window, bool) {
	for _, w := range rt.started {
		if w.Name == name {
			return w, true
		}
	}
	return runtime.Window{}, false
}

func (h *harness) load(t *testing.T, name string) *task.Task {
	t.Helper()
	tk, err := h.app.Store.Load(name)
	if err != nil {
		t.Fatal(err)
	}
	return tk
}

func TestSingleRepoTaskRunsItsAgentInTheRepo(t *testing.T) {
	h := newHarness(t)
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-1", "--agent", "claude", "--agent", "--add-dir", "--agent", "{context_dir}", "--repo", "backend="+h.repo); err != nil {
		t.Fatal(err)
	}
	if tk := h.load(t, "PAY-1"); tk.AgentRepo != "backend" || tk.HasRootWindow() {
		t.Fatalf("layout: agent_repo=%q root=%v", tk.AgentRepo, tk.HasRootWindow())
	}
	rt := &layoutRuntime{}
	h.app.Runtime = rt
	if err := h.exec(t, h.app.newStartCmd(), "PAY-1"); err != nil {
		t.Fatal(err)
	}
	if len(rt.started) != 1 {
		t.Fatalf("started %+v, want only the repo window", rt.started)
	}
	w := rt.started[0]
	ctxDir := h.app.Store.ContextDir("PAY-1")
	if w.Name != "backend" || !slices.Equal(w.Command, []string{"claude", "--add-dir", ctxDir}) {
		t.Fatalf("window: %+v", w)
	}
	if info, err := os.Stat(ctxDir); err != nil || !info.IsDir() {
		t.Fatalf("context directory for --add-dir: %v", err)
	}

	// status and show say where the agent is, and that there is no root.
	h.out.Reset()
	if err := h.exec(t, h.app.newStatusCmd(), "PAY-1"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(h.out.String(), "(root)") || !strings.Contains(h.out.String(), "claude") {
		t.Fatalf("status:\n%s", h.out.String())
	}
	h.out.Reset()
	if err := h.exec(t, h.app.newTaskCmd(), "show", "PAY-1"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.out.String(), "(window backend, in its worktree)") {
		t.Fatalf("show:\n%s", h.out.String())
	}

	if err := h.exec(t, h.app.newRestartCmd(), "PAY-1", "--root"); err == nil || !strings.Contains(err.Error(), "tspace restart PAY-1 backend") {
		t.Fatalf("restart --root = %v", err)
	}
}

func TestWithRootAndSeveralReposKeepTheRootWindow(t *testing.T) {
	h := newHarness(t)
	other := mkRepo(t)
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-1", "--with-root", "--agent", "claude", "--repo", "backend="+h.repo); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-2", "--agent", "claude", "--repo", "backend="+h.repo, "--repo", "frontend="+other); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"PAY-1", "PAY-2"} {
		rt := &layoutRuntime{}
		h.app.Runtime = rt
		if err := h.exec(t, h.app.newStartCmd(), name); err != nil {
			t.Fatal(err)
		}
		root, ok := rt.window(task.RootWindowName)
		if !ok || !slices.Equal(root.Command, []string{"claude"}) {
			t.Fatalf("%s root window: %+v", name, rt.started)
		}
		if b, _ := rt.window("backend"); len(b.Command) != 0 {
			t.Fatalf("%s backend runs %v, want a shell", name, b.Command)
		}
	}
}

// The second repository opens the root window in the running session and
// leaves the agent where it is, so its conversation is not lost.
func TestSecondRepoOpensRootWindowInRunningSession(t *testing.T) {
	h := newHarness(t)
	other := mkRepo(t)
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-1", "--agent", "claude", "--repo", "backend="+h.repo); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newStartCmd(), "PAY-1", "--no-runtime"); err != nil {
		t.Fatal(err)
	}
	rt := &layoutRuntime{running: true}
	h.app.Runtime = rt
	if err := h.exec(t, h.app.newRepoCmd(), "add", "PAY-1", other, "--name", "frontend"); err != nil {
		t.Fatal(err)
	}
	tk := h.load(t, "PAY-1")
	if tk.AgentRepo != "backend" || !tk.RootWindow {
		t.Fatalf("layout: agent_repo=%q root_window=%v", tk.AgentRepo, tk.RootWindow)
	}
	if len(rt.opened) != 1 || rt.opened[0].Name != task.RootWindowName || len(rt.opened[0].Command) != 0 {
		t.Fatalf("opened %+v, want a root shell", rt.opened)
	}
	if _, err := os.Stat(rt.opened[0].Dir); err != nil {
		t.Fatalf("root window directory: %v", err)
	}

	if err := h.exec(t, h.app.newStartCmd(), "PAY-1"); err != nil {
		t.Fatal(err)
	}
	if b, _ := rt.window("backend"); len(b.Command) == 0 || b.Command[0] != "claude" {
		t.Fatalf("backend runs %v, want the agent to stay", b.Command)
	}
	if f, _ := rt.window("frontend"); len(f.Command) != 0 {
		t.Fatalf("frontend runs %v, want a shell", f.Command)
	}

	// Back to one repository, the root window stays.
	if err := h.exec(t, h.app.newRepoCmd(), "rm", "PAY-1", "frontend"); err != nil {
		t.Fatal(err)
	}
	if !h.load(t, "PAY-1").HasRootWindow() {
		t.Fatal("removing a repository took the root window away")
	}
}

func TestSecondRepoWithoutSessionLeavesRootToStart(t *testing.T) {
	h := newHarness(t)
	other := mkRepo(t)
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-1", "--repo", "backend="+h.repo); err != nil {
		t.Fatal(err)
	}
	rt := &layoutRuntime{}
	h.app.Runtime = rt
	if err := h.exec(t, h.app.newRepoCmd(), "add", "PAY-1", other, "--name", "frontend"); err != nil {
		t.Fatal(err)
	}
	if len(rt.opened) != 0 || !strings.Contains(h.out.String(), "the next start opens a root window") {
		t.Fatalf("opened %+v\n%s", rt.opened, h.out.String())
	}
	if err := h.exec(t, h.app.newStartCmd(), "PAY-1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := rt.window(task.RootWindowName); !ok {
		t.Fatalf("start did not create the root window: %+v", rt.started)
	}
}

func TestRootWindowThatCannotOpenIsLeftToStart(t *testing.T) {
	h := newHarness(t)
	other := mkRepo(t)
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-1", "--repo", "backend="+h.repo); err != nil {
		t.Fatal(err)
	}
	h.app.Runtime = &layoutRuntime{running: true, openErr: errors.New("needs a client")}
	if err := h.exec(t, h.app.newRepoCmd(), "add", "PAY-1", other, "--name", "frontend"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.errb.String(), "the next tspace start PAY-1 will") {
		t.Fatalf("stderr: %s", h.errb.String())
	}
	if !h.load(t, "PAY-1").RootWindow {
		t.Fatal("root window not recorded")
	}
}

// A task created empty takes the layout from its first repository, unless
// a session is running that a root agent could be in.
func TestFirstRepoOfEmptyTaskChoosesTheLayout(t *testing.T) {
	h := newHarness(t)
	for _, running := range []bool{false, true} {
		name := "PAY-idle"
		if running {
			name = "PAY-running"
		}
		if err := h.exec(t, h.app.newTaskCmd(), "create", name); err != nil {
			t.Fatal(err)
		}
		h.app.Runtime = &layoutRuntime{running: running}
		if err := h.exec(t, h.app.newRepoCmd(), "add", name, h.repo, "--name", "backend"); err != nil {
			t.Fatal(err)
		}
		tk := h.load(t, name)
		if got, want := tk.AgentRepo, map[bool]string{false: "backend", true: ""}[running]; got != want {
			t.Errorf("running=%v: agent_repo=%q, want %q", running, got, want)
		}
	}
}

// A session that cannot be checked may be running the agent in the root
// window: a first repository does not take the agent, and a second does not
// open a root window in it.
func TestRepoAddWithASessionItCannotCheck(t *testing.T) {
	h := newHarness(t)
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-1"); err != nil {
		t.Fatal(err)
	}
	rt := &layoutRuntime{hasErr: errors.New("server busy")}
	h.app.Runtime = rt
	if err := h.exec(t, h.app.newRepoCmd(), "add", "PAY-1", h.repo, "--name", "backend"); err != nil {
		t.Fatal(err)
	}
	if tk := h.load(t, "PAY-1"); tk.AgentRepo != "" {
		t.Fatalf("agent moved to %q under a session that may be running", tk.AgentRepo)
	}
	if !strings.Contains(h.errb.String(), "server busy") {
		t.Fatalf("no warning: %q", h.errb.String())
	}

	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-2", "--repo", "backend="+h.repo); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newRepoCmd(), "add", "PAY-2", mkRepo(t), "--name", "frontend"); err != nil {
		t.Fatal(err)
	}
	if tk := h.load(t, "PAY-2"); !tk.RootWindow || len(rt.opened) != 0 {
		t.Fatalf("root=%v opened=%v", tk.RootWindow, rt.opened)
	}
	if !strings.Contains(h.errb.String(), "opens when the task next starts") {
		t.Fatalf("no warning for the second repository: %q", h.errb.String())
	}
}

// Nor does repo rm --keep-worktree move the agent out of a window that may
// still run it.
func TestRepoRmRefusesASessionItCannotCheck(t *testing.T) {
	h := newHarness(t)
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-1", "--agent", "claude", "--repo", "backend="+h.repo); err != nil {
		t.Fatal(err)
	}
	h.app.Runtime = &layoutRuntime{hasErr: errors.New("server busy")}
	err := h.exec(t, h.app.newRepoCmd(), "rm", "PAY-1", "backend", "--keep-worktree")
	if err == nil || !strings.Contains(err.Error(), "server busy") || !strings.Contains(err.Error(), "tspace stop PAY-1") {
		t.Fatalf("rm = %v", err)
	}
	if _, ok := h.load(t, "PAY-1").Repo("backend"); !ok {
		t.Fatal("refused rm still removed the repo")
	}
}

// Removing the agent's repository moves the agent to the root window.
func TestRemovingAgentRepoMovesAgentToRoot(t *testing.T) {
	h := newHarness(t)
	other := mkRepo(t)
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-1", "--agent", "claude", "--repo", "backend="+h.repo); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newRepoCmd(), "add", "PAY-1", other, "--name", "frontend"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newRepoCmd(), "rm", "PAY-1", "backend"); err != nil {
		t.Fatal(err)
	}
	tk := h.load(t, "PAY-1")
	if tk.AgentRepo != "" || !slices.Equal(tk.RootAgent(), []string{"claude"}) {
		t.Fatalf("agent_repo=%q root agent=%v", tk.AgentRepo, tk.RootAgent())
	}
}

// The agent window's own hook reports for that window.
func TestSignalFromTheAgentRepoWindow(t *testing.T) {
	h := newHarness(t)
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-1", "--agent", "claude", "--repo", "backend="+h.repo); err != nil {
		t.Fatal(err)
	}
	rt := &layoutRuntime{}
	h.app.Runtime = rt
	if err := h.exec(t, h.app.newStartCmd(), "PAY-1"); err != nil {
		t.Fatal(err)
	}
	for _, kv := range rt.started[0].Env {
		k, v, _ := strings.Cut(kv, "=")
		t.Setenv(k, v)
	}
	// With a root window too, the task directory alone would mean _root: only
	// the window's environment says backend.
	tk := h.load(t, "PAY-1")
	tk.RootWindow = true
	if err := h.app.Store.Save(tk); err != nil {
		t.Fatal(err)
	}
	chdir(t, h.app.Store.TaskDir("PAY-1"))
	if err := h.exec(t, h.app.newSignalCmd(), "blocked"); err != nil {
		t.Fatal(err)
	}
	if got := h.signals(t, "PAY-1")["backend"]; got.State != task.SignalBlocked {
		t.Fatalf("signal: %+v", h.signals(t, "PAY-1"))
	}

	// Started somewhere else from that window, an agent is not the window's.
	chdir(t, t.TempDir())
	if err := h.exec(t, h.app.newSignalCmd(), "-q", "done"); err != nil {
		t.Fatal(err)
	}
	if got := h.signals(t, "PAY-1")["backend"]; got.State != task.SignalBlocked {
		t.Fatalf("an agent outside the task reported for its window: %+v", got)
	}
}

// Without a root row, dash names a single-repo task on its repository's row.
func TestDashNamesSingleRepoTask(t *testing.T) {
	h := newHarness(t)
	h.startedTask(t, "PAY-1")
	h.app.Runtime = &layoutRuntime{running: true}
	m := newDashModel(context.Background(), h.app, time.Second)
	m = drive(t, m, dashDataMsg(h.app.dashLoad(context.Background())))
	if len(m.data.Rows) != 1 || m.data.Rows[0].root() {
		t.Fatalf("rows: %+v", m.data.Rows)
	}
	if view := m.View(); !strings.Contains(view, "PAY-1") || strings.Contains(view, "(root)") {
		t.Fatalf("view:\n%s", view)
	}
}

// The agent repository's agent is the task's: --shell makes its window a
// shell, and --agent replaces what runs there.
func TestRepoUpdateOfAgentRepoChangesTheTaskAgent(t *testing.T) {
	h := newHarness(t)
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-1", "--agent", "claude", "--repo", "backend="+h.repo); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newRepoCmd(), "update", "PAY-1", "backend", "--agent", "codex"); err != nil {
		t.Fatal(err)
	}
	tk := h.load(t, "PAY-1")
	if !slices.Equal(tk.Agent, []string{"codex"}) || len(tk.Repos["backend"].Agent) != 0 {
		t.Fatalf("agent=%v repo agent=%v", tk.Agent, tk.Repos["backend"].Agent)
	}
}

// --shell on the agent repository would leave the task with no agent and
// no resume command, so it refuses and changes nothing.
func TestRepoUpdateShellRefusesToDropTheTaskAgent(t *testing.T) {
	h := newHarness(t)
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-1", "--agent", "claude", "--resume", "claude", "--resume=--continue", "--repo", "backend="+h.repo); err != nil {
		t.Fatal(err)
	}
	err := h.exec(t, h.app.newRepoCmd(), "update", "PAY-1", "backend", "--shell", "--post-create", "make")
	if err == nil || !strings.Contains(err.Error(), "tspace task update PAY-1 --shell") {
		t.Fatalf("update = %v", err)
	}
	tk := h.load(t, "PAY-1")
	if !slices.Equal(tk.Agent, []string{"claude"}) || !slices.Equal(tk.Resume, []string{"claude", "--continue"}) || tk.AgentRepo != "backend" || tk.Repos["backend"].PostCreate != "" {
		t.Fatalf("agent=%v resume=%v agent_repo=%q post_create=%q", tk.Agent, tk.Resume, tk.AgentRepo, tk.Repos["backend"].PostCreate)
	}
}

// With nothing to lose, --shell on the agent repository goes through, and
// so does the rest of the update.
func TestRepoUpdateShellOnTheAgentRepoWithoutAnAgent(t *testing.T) {
	h := newHarness(t)
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-1", "--shell", "--repo", "backend="+h.repo); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newRepoCmd(), "update", "PAY-1", "backend", "--shell", "--post-create", "make"); err != nil {
		t.Fatal(err)
	}
	if tk := h.load(t, "PAY-1"); tk.AgentRepo != "backend" || tk.Repos["backend"].PostCreate != "make" {
		t.Fatalf("agent_repo=%q post_create=%q", tk.AgentRepo, tk.Repos["backend"].PostCreate)
	}
}

// An agent of the repository's own, in the agent repository, is what --shell
// clears; the window then runs the task's agent again.
func TestRepoUpdateShellClearsTheAgentRepoOwnAgent(t *testing.T) {
	h := newHarness(t)
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-1", "--agent", "claude", "--repo", "backend="+h.repo); err != nil {
		t.Fatal(err)
	}
	tk := h.load(t, "PAY-1")
	r := tk.Repos["backend"]
	r.Agent = []string{"aider"}
	tk.SetRepo("backend", r)
	if err := h.app.Store.Save(tk); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newRepoCmd(), "update", "PAY-1", "backend", "--shell"); err != nil {
		t.Fatal(err)
	}
	tk = h.load(t, "PAY-1")
	if !slices.Equal(tk.Agent, []string{"claude"}) || len(tk.Repos["backend"].Agent) != 0 {
		t.Fatalf("agent=%v repo agent=%v", tk.Agent, tk.Repos["backend"].Agent)
	}
}

// A repository with an agent of its own keeps the root layout, so the
// task's agent still has somewhere to run.
func TestRepoWithItsOwnAgentKeepsTheRootWindow(t *testing.T) {
	h := newHarness(t)
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-1"); err != nil {
		t.Fatal(err)
	}
	h.app.Runtime = &layoutRuntime{}
	if err := h.exec(t, h.app.newRepoCmd(), "add", "PAY-1", h.repo, "--name", "backend", "--agent", "codex"); err != nil {
		t.Fatal(err)
	}
	if tk := h.load(t, "PAY-1"); tk.AgentRepo != "" || !tk.HasRootWindow() {
		t.Fatalf("agent_repo=%q root=%v", tk.AgentRepo, tk.HasRootWindow())
	}
}

func TestRepoRmRefusesToOrphanARunningAgent(t *testing.T) {
	h := newHarness(t)
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-1", "--agent", "claude", "--repo", "backend="+h.repo); err != nil {
		t.Fatal(err)
	}
	h.app.Runtime = &layoutRuntime{running: true}
	if err := h.exec(t, h.app.newRepoCmd(), "rm", "PAY-1", "backend", "--keep-worktree"); err == nil || !strings.Contains(err.Error(), "tspace stop PAY-1") {
		t.Fatalf("rm = %v", err)
	}
	if _, ok := h.load(t, "PAY-1").Repo("backend"); !ok {
		t.Fatal("refused rm still removed the repo")
	}
}

// Emptied by repo rm, a root-layout task keeps its root window: its agent's
// history is in the task directory.
func TestEmptiedTaskKeepsTheRootLayout(t *testing.T) {
	h := newHarness(t)
	other := mkRepo(t)
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-1", "--agent", "claude", "--repo", "backend="+h.repo, "--repo", "frontend="+other); err != nil {
		t.Fatal(err)
	}
	h.app.Runtime = &layoutRuntime{}
	for _, name := range []string{"backend", "frontend"} {
		if err := h.exec(t, h.app.newRepoCmd(), "rm", "PAY-1", name); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.exec(t, h.app.newRepoCmd(), "add", "PAY-1", h.repo, "--name", "backend"); err != nil {
		t.Fatal(err)
	}
	if tk := h.load(t, "PAY-1"); tk.AgentRepo != "" {
		t.Fatalf("agent moved into %s", tk.AgentRepo)
	}
}

// With no root window, the task directory means the agent's window, and
// --root has nothing to report for.
func TestSignalWithoutRootWindow(t *testing.T) {
	h := newHarness(t)
	h.startedTask(t, "PAY-1")
	tk := h.load(t, "PAY-1")
	chdir(t, h.app.Store.TaskDir("PAY-1"))
	if err := h.exec(t, h.app.newSignalCmd(), "blocked"); err != nil {
		t.Fatal(err)
	}
	if got := h.signals(t, "PAY-1")[tk.AgentRepo]; got.State != task.SignalBlocked {
		t.Fatalf("signals: %+v", h.signals(t, "PAY-1"))
	}
	if err := h.exec(t, h.app.newSignalCmd(), "done", "--root"); err == nil || !strings.Contains(err.Error(), "--repo "+tk.AgentRepo) {
		t.Fatalf("--root = %v", err)
	}
}

func TestTaskJSONReportsTheAgentWindow(t *testing.T) {
	h := newHarness(t)
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-1", "--agent", "claude", "--repo", "backend="+h.repo); err != nil {
		t.Fatal(err)
	}
	got := h.app.describeTask(h.load(t, "PAY-1"))
	if got.AgentWindow != "backend" || got.RootWindow || !slices.Equal(got.Repos[0].Agent, []string{"claude"}) {
		t.Fatalf("json: %+v", got)
	}
}

// Any agent can name the task's directories; tspace knows no agent by name.
func TestAgentCommandPlaceholders(t *testing.T) {
	h := newHarness(t)
	other := mkRepo(t)
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-1", "--agent", "some-agent", "--agent", "--notes={context_dir}/TASK.md",
		"--repo", "backend="+h.repo, "--repo", "frontend="+other); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newRepoCmd(), "update", "PAY-1", "frontend", "--agent", "other-agent", "--agent", "{task_dir}"); err != nil {
		t.Fatal(err)
	}
	rt := &layoutRuntime{}
	h.app.Runtime = rt
	if err := h.exec(t, h.app.newStartCmd(), "PAY-1"); err != nil {
		t.Fatal(err)
	}
	root, _ := rt.window(task.RootWindowName)
	if want := []string{"some-agent", "--notes=" + h.app.Store.ContextDir("PAY-1") + "/TASK.md"}; !slices.Equal(root.Command, want) {
		t.Fatalf("root runs %v, want %v", root.Command, want)
	}
	front, _ := rt.window("frontend")
	if want := []string{"other-agent", h.app.Store.TaskDir("PAY-1")}; !slices.Equal(front.Command, want) {
		t.Fatalf("frontend runs %v, want %v", front.Command, want)
	}
	// The record keeps the placeholders, so moving TSPACE_ROOT moves them too.
	if got := h.load(t, "PAY-1").Agent; got[1] != "--notes={context_dir}/TASK.md" {
		t.Fatalf("recorded agent %v", got)
	}
}

// --agent-at-root moves the task's agent out of its repository's worktree,
// keeping the agent and its resume command; the repository's window goes
// back to a shell, and the root window stays from then on.
func TestTaskUpdateMovesTheAgentToTheRoot(t *testing.T) {
	h := newHarness(t)
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-1", "--agent", "claude", "--resume", "claude", "--resume=--continue", "--repo", "backend="+h.repo); err != nil {
		t.Fatal(err)
	}
	if err := h.app.Store.MarkLaunched("PAY-1", "backend"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newTaskCmd(), "update", "PAY-1", "--agent-at-root"); err != nil {
		t.Fatal(err)
	}
	tk := h.load(t, "PAY-1")
	if tk.AgentRepo != "" || !tk.RootWindow || !slices.Equal(tk.Agent, []string{"claude"}) || !slices.Equal(tk.Resume, []string{"claude", "--continue"}) {
		t.Fatalf("agent_repo=%q root=%v agent=%v resume=%v", tk.AgentRepo, tk.RootWindow, tk.Agent, tk.Resume)
	}
	if len(tk.RepoAgent("backend")) != 0 || !slices.Equal(tk.RootAgent(), []string{"claude"}) {
		t.Fatalf("backend runs %v, root runs %v", tk.RepoAgent("backend"), tk.RootAgent())
	}
	if out := h.out.String(); !strings.Contains(out, "agent window: _root (was backend, which now runs shell)") {
		t.Fatalf("out:\n%s", out)
	}
	if e := h.errb.String(); !strings.Contains(e, "conversation from "+tk.Repos["backend"].Worktree+" stays there") {
		t.Fatalf("no conversation note:\n%s", e)
	}
	// A first repository added later no longer takes the agent into its worktree.
	if err := h.exec(t, h.app.newRepoCmd(), "rm", "PAY-1", "backend"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newRepoCmd(), "add", "PAY-1", "backend="+h.repo); err != nil {
		t.Fatal(err)
	}
	if tk := h.load(t, "PAY-1"); tk.AgentRepo != "" {
		t.Fatalf("agent moved back into %s", tk.AgentRepo)
	}
}

// In a running session nothing is restarted: the command says which window
// keeps the agent and how the root window gets it.
func TestTaskUpdateAgentAtRootInARunningSession(t *testing.T) {
	h := newHarness(t)
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-1", "--agent", "claude", "--repo", "backend="+h.repo); err != nil {
		t.Fatal(err)
	}
	rt := &layoutRuntime{running: true}
	h.app.Runtime = rt
	if err := h.exec(t, h.app.newTaskCmd(), "update", "PAY-1", "--agent-at-root"); err != nil {
		t.Fatal(err)
	}
	out := h.out.String()
	for _, want := range []string{"backend keeps its agent until restarted (tspace restart PAY-1 backend", "tspace start PAY-1 opens the root window"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	if len(rt.opened) != 0 || len(rt.started) != 0 {
		t.Fatalf("windows changed: opened=%v started=%v", rt.opened, rt.started)
	}
	// With a root window open already, restarting it is what runs the agent.
	h.app.Runtime = &stubRuntime{}
	if err := h.exec(t, h.app.newTaskCmd(), "update", "PAY-1", "--agent-in", "backend"); err != nil {
		t.Fatal(err)
	}
	h.app.Runtime = rt
	if err := h.exec(t, h.app.newTaskCmd(), "update", "PAY-1", "--agent-at-root"); err != nil {
		t.Fatal(err)
	}
	if out := h.out.String(); !strings.Contains(out, "_root runs the agent once restarted (tspace restart PAY-1 --root") {
		t.Fatalf("out:\n%s", out)
	}
}

// --agent-in moves the agent into a repository's worktree, keeping the root
// window there was as a shell, and refuses a repository with its own agent.
func TestTaskUpdateMovesTheAgentIntoARepo(t *testing.T) {
	h := newHarness(t)
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-1", "--agent", "claude", "--with-root", "--repo", "backend="+h.repo); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newTaskCmd(), "update", "PAY-1", "--agent-in", "backend"); err != nil {
		t.Fatal(err)
	}
	tk := h.load(t, "PAY-1")
	if tk.AgentRepo != "backend" || !tk.HasRootWindow() || len(tk.RootAgent()) != 0 || !slices.Equal(tk.RepoAgent("backend"), []string{"claude"}) {
		t.Fatalf("agent_repo=%q root=%v root agent=%v backend agent=%v", tk.AgentRepo, tk.HasRootWindow(), tk.RootAgent(), tk.RepoAgent("backend"))
	}
	if err := h.exec(t, h.app.newTaskCmd(), "update", "PAY-1", "--agent-in", "backend"); err != nil {
		t.Fatal(err)
	}
	if out := h.out.String(); !strings.Contains(out, "agent window: backend (unchanged)") {
		t.Fatalf("out:\n%s", out)
	}

	if err := h.exec(t, h.app.newRepoCmd(), "update", "PAY-1", "backend", "--post-create", "make"); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"--agent-in", "frontend"}, `no repo named "frontend"`},
		{[]string{"--agent-in", "_root"}, "use --agent-at-root"},
		{[]string{"--agent-in", "backend", "--agent-at-root"}, "choose either"},
	} {
		err := h.exec(t, h.app.newTaskCmd(), append([]string{"update", "PAY-1"}, c.args...)...)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%v: %v", c.args, err)
		}
	}
}

// A repository with its own agent cannot run the task's.
func TestTaskUpdateAgentInRefusesARepoWithItsOwnAgent(t *testing.T) {
	h := newHarness(t)
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-1", "--agent", "claude"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newRepoCmd(), "add", "PAY-1", "backend="+h.repo, "--agent", "codex"); err != nil {
		t.Fatal(err)
	}
	err := h.exec(t, h.app.newTaskCmd(), "update", "PAY-1", "--agent-in", "backend", "--title", "x")
	if err == nil || !strings.Contains(err.Error(), "tspace repo update PAY-1 backend --shell") {
		t.Fatalf("update = %v", err)
	}
	if tk := h.load(t, "PAY-1"); tk.AgentRepo != "" || tk.Title != "" {
		t.Fatalf("changed: agent_repo=%q title=%q", tk.AgentRepo, tk.Title)
	}
}
