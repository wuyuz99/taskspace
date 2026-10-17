package cli

import (
	"context"
	"errors"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wuyuz99/taskspace/internal/runtime"
	"github.com/wuyuz99/taskspace/internal/task"
)

// TestMain lets the test binary stand in for the user's shell: `tspace shell`
// runs $SHELL with no arguments, which no real shell survives without a
// terminal, and a script would not run on Windows.
func TestMain(m *testing.M) {
	if out := os.Getenv("TSPACE_TEST_SHELL_OUT"); out != "" {
		wd, _ := os.Getwd()
		env := []string{"PWD=" + wd}
		for _, kv := range os.Environ() {
			if strings.HasPrefix(kv, "TSPACE_") && !strings.HasPrefix(kv, "TSPACE_TEST_") {
				env = append(env, kv)
			}
		}
		_ = os.WriteFile(out, []byte(strings.Join(env, "\n")), 0o644)
		if os.Getenv("TSPACE_TEST_SHELL_FAIL") != "" {
			os.Exit(3)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// fakeShell makes $SHELL the test binary and returns where it records what it saw.
func fakeShell(t *testing.T) string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "shell-env")
	t.Setenv("SHELL", self)
	t.Setenv("TSPACE_TEST_SHELL_OUT", out)
	return out
}

func readShellEnv(t *testing.T, path string) map[string]string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the shell never ran: %v", err)
	}
	env := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			env[k] = v
		}
	}
	return env
}

func TestShellOpensTheWorktreeWithTaskEnvironment(t *testing.T) {
	h := newHarness(t)
	tk := h.startedTask(t, "PAY-1")
	repo := tk.RepoNames()[0]
	if err := h.app.Store.WriteNote("PAY-1", task.BriefFile, "Reject expired cards.\n"); err != nil {
		t.Fatal(err)
	}
	out := fakeShell(t)

	if err := h.exec(t, h.app.newShellCmd(), "PAY-1"); err != nil {
		t.Fatalf("shell: %v", err)
	}
	env := readShellEnv(t, out)
	wt := tk.Repos[repo].Worktree
	if !samePath(env["PWD"], wt) {
		t.Errorf("shell ran in %q, want %q", env["PWD"], wt)
	}
	want := map[string]string{
		"TSPACE_TASK":        "PAY-1",
		"TSPACE_REPO":        repo,
		"TSPACE_WORKTREE":    wt,
		"TSPACE_TASK_DIR":    h.app.Store.TaskDir("PAY-1"),
		"TSPACE_CONTEXT_DIR": h.app.Store.ContextDir("PAY-1"),
	}
	for k, v := range want {
		if env[k] != v {
			t.Errorf("%s = %q, want %q", k, env[k], v)
		}
	}
	// The brief is shown on the way in.
	if !strings.Contains(h.out.String(), "Reject expired cards.") {
		t.Errorf("brief not shown:\n%s", h.out.String())
	}
	if !strings.Contains(h.errb.String(), "tspace: PAY-1/"+repo+" in "+wt) {
		t.Errorf("banner = %q", h.errb.String())
	}
}

func TestShellRootCreatesTheTaskDirectory(t *testing.T) {
	h := newHarness(t)
	if _, err := h.app.Store.Create("empty"); err != nil {
		t.Fatal(err)
	}
	out := fakeShell(t)

	if err := h.exec(t, h.app.newShellCmd(), "empty", "--root"); err != nil {
		t.Fatalf("shell --root: %v", err)
	}
	env := readShellEnv(t, out)
	dir := h.app.Store.TaskDir("empty")
	if !samePath(env["PWD"], dir) || env["TSPACE_REPO"] != "" {
		t.Errorf("root shell: PWD=%q REPO=%q, want %q and no repo", env["PWD"], env["TSPACE_REPO"], dir)
	}
}

// A shell exiting non-zero is how shells exit, not a tspace error.
func TestShellNonZeroExitIsNotAnError(t *testing.T) {
	h := newHarness(t)
	h.startedTask(t, "PAY-1")
	fakeShell(t)
	t.Setenv("TSPACE_TEST_SHELL_FAIL", "1")
	if err := h.exec(t, h.app.newShellCmd(), "PAY-1"); err != nil {
		t.Fatalf("shell exiting 3 = %v, want nil", err)
	}
}

func TestShellRefusals(t *testing.T) {
	h := newHarness(t)
	tk := h.startedTask(t, "PAY-1")
	fakeShell(t)

	if err := h.exec(t, h.app.newShellCmd(), "PAY-1", tk.RepoNames()[0], "--root"); err == nil || !strings.Contains(err.Error(), "--root cannot be combined") {
		t.Errorf("--root with a repo = %v", err)
	}
	if err := h.exec(t, h.app.newShellCmd(), "PAY-1", "nope"); err == nil {
		t.Error("unknown repo accepted")
	}
	if err := h.exec(t, h.app.newShellCmd(), "missing"); err == nil {
		t.Error("unknown task accepted")
	}

	// A shell that cannot be started at all is a real error.
	t.Setenv("SHELL", filepath.Join(t.TempDir(), "no-such-shell"))
	if err := h.exec(t, h.app.newShellCmd(), "PAY-1"); err == nil {
		t.Error("missing shell reported no error")
	}
}

func TestExecRunsInEveryWorktree(t *testing.T) {
	h := newHarness(t)
	other := mkRepo(t)
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-2", "--repo", "backend="+h.repo, "--repo", "frontend="+other); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newStartCmd(), "PAY-2", "--no-runtime"); err != nil {
		t.Fatal(err)
	}
	tk, err := h.app.Store.Load("PAY-2")
	if err != nil {
		t.Fatal(err)
	}

	if err := h.exec(t, h.app.newExecCmd(), "PAY-2", "--", "git", "rev-parse", "--abbrev-ref", "HEAD"); err != nil {
		t.Fatalf("exec: %v\n%s", err, h.errb.String())
	}
	got := h.out.String()
	// Repo-name order, each headed, each on the task's branch.
	be, fe := strings.Index(got, "=== backend"), strings.Index(got, "=== frontend")
	if be < 0 || fe < be {
		t.Fatalf("output not in repo order:\n%s", got)
	}
	if n := strings.Count(got, tk.Repos["backend"].Branch); n != 2 {
		t.Errorf("branch printed %d times, want 2:\n%s", n, got)
	}

	// A failure in one repo is reported; by default the rest still run.
	if err := os.RemoveAll(tk.Repos["backend"].Worktree); err != nil {
		t.Fatal(err)
	}
	err = h.exec(t, h.app.newExecCmd(), "PAY-2", "--", "git", "status")
	if err == nil || !strings.Contains(err.Error(), "failed in: backend") {
		t.Fatalf("exec with a missing worktree = %v", err)
	}
	if !strings.Contains(h.errb.String(), "backend: worktree missing, skipped") || !strings.Contains(h.out.String(), "=== frontend") {
		t.Fatalf("missing worktree not skipped:\nout: %s\nerr: %s", h.out.String(), h.errb.String())
	}
}

func TestExecStopsAtFirstFailureWhenAsked(t *testing.T) {
	h := newHarness(t)
	other := mkRepo(t)
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-3", "--repo", "a="+h.repo, "--repo", "b="+other); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newStartCmd(), "PAY-3", "--no-runtime"); err != nil {
		t.Fatal(err)
	}

	err := h.exec(t, h.app.newExecCmd(), "PAY-3", "--keep-going=false", "--", "git", "no-such-subcommand")
	if err == nil || !strings.Contains(err.Error(), "failed in: a") || strings.Contains(err.Error(), "b") {
		t.Fatalf("exec --keep-going=false = %v, want failure in a only", err)
	}
	if strings.Contains(h.out.String(), "=== b") {
		t.Fatalf("ran b after a failed:\n%s", h.out.String())
	}

	err = h.exec(t, h.app.newExecCmd(), "PAY-3", "--", "git", "no-such-subcommand")
	if err == nil || !strings.Contains(err.Error(), "failed in: a, b") {
		t.Fatalf("exec --keep-going = %v, want failures in both", err)
	}
}

func TestExecRefusals(t *testing.T) {
	h := newHarness(t)
	if _, err := h.app.Store.Create("empty"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newExecCmd(), "empty"); err == nil || !strings.Contains(err.Error(), "no command given") {
		t.Errorf("exec without a command = %v", err)
	}
	if err := h.exec(t, h.app.newExecCmd(), "empty", "--", "git", "status"); err == nil || !strings.Contains(err.Error(), "has no repositories") {
		t.Errorf("exec on an empty task = %v", err)
	}
	if err := h.exec(t, h.app.newExecCmd(), "missing", "--", "git"); err == nil {
		t.Error("unknown task accepted")
	}
}

func TestPostCreateRunsOnceWithItsEnvironment(t *testing.T) {
	if _, err := osexec.LookPath("sh"); err != nil {
		t.Skip("sh not on PATH")
	}
	h := newHarness(t)
	if _, err := h.app.Store.Create("PAY-4"); err != nil {
		t.Fatal(err)
	}
	hook := `printf '%s|%s|%s' "$TSPACE_TASK" "$TSPACE_REPO" "$TSPACE_REPO_PATH" > hook.txt`
	if err := h.exec(t, h.app.newRepoCmd(), "add", "PAY-4", h.repo, "--post-create", hook); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newStartCmd(), "PAY-4", "--no-runtime"); err != nil {
		t.Fatalf("start: %v\n%s", err, h.errb.String())
	}
	if !strings.Contains(h.out.String(), "post_create: "+hook) {
		t.Errorf("hook not announced:\n%s", h.out.String())
	}
	tk, err := h.app.Store.Load("PAY-4")
	if err != nil {
		t.Fatal(err)
	}
	name := tk.RepoNames()[0]
	r := tk.Repos[name]
	b, err := os.ReadFile(filepath.Join(r.Worktree, "hook.txt"))
	if err != nil {
		t.Fatalf("hook did not run in the worktree: %v", err)
	}
	if want := "PAY-4|" + name + "|" + r.Path; string(b) != want {
		t.Errorf("hook saw %q, want %q", b, want)
	}
}

func TestPostCreateFailureIsReported(t *testing.T) {
	if _, err := osexec.LookPath("sh"); err != nil {
		t.Skip("sh not on PATH")
	}
	h := newHarness(t)
	tk := task.New("PAY-5")
	r := task.Repo{Path: h.repo, Worktree: t.TempDir(), PostCreate: "exit 7"}
	err := h.app.runPostCreate(t.Context(), tk, "backend", r)
	var exit *osexec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 7 {
		t.Fatalf("runPostCreate = %v, want exit 7", err)
	}
}

func TestStop(t *testing.T) {
	h := newHarness(t)
	tk := h.startedTask(t, "PAY-6")
	repo := tk.RepoNames()[0]

	// Nothing running: stale signals are cleared and nothing is stopped.
	if err := h.app.Store.WriteSignal("PAY-6", repo, task.SignalWorking, ""); err != nil {
		t.Fatal(err)
	}
	stub := h.app.Runtime.(*stubRuntime)
	if err := h.exec(t, h.app.newStopCmd(), "PAY-6"); err != nil {
		t.Fatal(err)
	}
	if stub.stopped || !strings.Contains(h.out.String(), "nothing running for PAY-6") {
		t.Fatalf("stop with nothing running: stopped=%v out=%q", stub.stopped, h.out.String())
	}
	if sigs := h.signals(t, "PAY-6"); len(sigs) != 0 {
		t.Fatalf("stale signals survived: %v", sigs)
	}

	// Running: the session is stopped and its signals go with it.
	rt := &windowRuntime{running: true, panes: map[string]runtime.PaneState{}}
	h.app.Runtime = &runningRuntime{rt}
	if err := h.app.Store.WriteSignal("PAY-6", repo, task.SignalBlocked, "?"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newStopCmd(), "PAY-6"); err != nil {
		t.Fatal(err)
	}
	if !rt.stopped || !strings.Contains(h.out.String(), "stopped PAY-6; worktrees left in place") {
		t.Fatalf("stop: stopped=%v out=%q", rt.stopped, h.out.String())
	}
	if sigs := h.signals(t, "PAY-6"); len(sigs) != 0 {
		t.Fatalf("signals survived stop: %v", sigs)
	}
	if _, err := os.Stat(tk.Repos[repo].Worktree); err != nil {
		t.Fatalf("stop touched the worktree: %v", err)
	}

	if err := h.exec(t, h.app.newStopCmd(), "missing"); err == nil {
		t.Error("unknown task accepted")
	}
}

// runningRuntime reports a live session, which stubRuntime never does.
type runningRuntime struct{ *windowRuntime }

func (r *runningRuntime) HasSession(context.Context, string) (bool, error) { return true, nil }

func TestRepoRm(t *testing.T) {
	h := newHarness(t)
	tk := h.startedTask(t, "PAY-7")
	repo := tk.RepoNames()[0]
	wt := tk.Repos[repo].Worktree

	if err := h.exec(t, h.app.newRepoCmd(), "rm", "PAY-7", "nope"); err == nil || !strings.Contains(err.Error(), `no repo named "nope"`) {
		t.Fatalf("rm of an unknown repo = %v", err)
	}
	if err := h.exec(t, h.app.newRepoCmd(), "rm", "missing", repo); err == nil {
		t.Fatal("rm on an unknown task accepted")
	}

	if err := h.exec(t, h.app.newRepoCmd(), "rm", "PAY-7", repo); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.out.String(), "removed "+repo+" from PAY-7") {
		t.Errorf("out = %q", h.out.String())
	}
	// rm forgets the repo but leaves the worktree for clean's safety checks.
	if !strings.Contains(h.errb.String(), "the worktree at "+wt+" was left in place") {
		t.Errorf("err = %q", h.errb.String())
	}
	got, err := h.app.Store.Load("PAY-7")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.Repo(repo); ok {
		t.Fatal("repo still recorded")
	}
	if _, err := os.Stat(wt); err != nil {
		t.Fatalf("worktree removed: %v", err)
	}
}

func TestTaskShow(t *testing.T) {
	h := newHarness(t)
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-8", "--agent", "claude"); err != nil {
		t.Fatal(err)
	}
	// With no repositories, show says how to add one.
	if err := h.exec(t, h.app.newTaskCmd(), "show", "PAY-8"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.out.String(), "no repositories yet") || !strings.Contains(h.out.String(), "tspace repo add PAY-8 REPO") {
		t.Fatalf("empty show:\n%s", h.out.String())
	}

	if err := h.exec(t, h.app.newRepoCmd(), "add", "PAY-8", h.repo, "--post-create", "make deps"); err != nil {
		t.Fatal(err)
	}
	tk, err := h.app.Store.Load("PAY-8")
	if err != nil {
		t.Fatal(err)
	}
	name := tk.RepoNames()[0]
	r := tk.Repos[name]

	if err := h.exec(t, h.app.newTaskCmd(), "show", "PAY-8"); err != nil {
		t.Fatal(err)
	}
	out := h.out.String()
	for _, want := range []string{
		"task:     PAY-8",
		"session:  tspace-PAY-8",
		"workspace: " + h.app.Store.TaskDir("PAY-8"),
		"context:   " + h.app.Store.ContextDir("PAY-8"),
		"\n" + name + "\n",
		"  repo:      " + r.Path,
		"  branch:    " + r.Branch,
		"  worktree:  " + r.Worktree,
		"  post_create: make deps",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("show lacks %q:\n%s", want, out)
		}
	}

	if err := h.exec(t, h.app.newTaskCmd(), "show", "PAY-8", "--json"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.out.String(), `"name": "PAY-8"`) {
		t.Errorf("json show:\n%s", h.out.String())
	}
	if err := h.exec(t, h.app.newTaskCmd(), "show", "missing"); err == nil {
		t.Error("unknown task accepted")
	}
}

// samePath compares paths the way the OS does, so a short-name or symlinked
// temp directory does not fail a test for an uninteresting reason.
func samePath(a, b string) bool {
	ai, err1 := os.Stat(a)
	bi, err2 := os.Stat(b)
	return err1 == nil && err2 == nil && os.SameFile(ai, bi)
}
