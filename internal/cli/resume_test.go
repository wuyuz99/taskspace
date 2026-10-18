package cli

import (
	"slices"
	"strings"
	"testing"

	"github.com/wuyuz99/taskspace/internal/task"
)

func TestRestartResumeRunsTheResumeCommand(t *testing.T) {
	h := newHarness(t)
	other := mkRepo(t)
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-1",
		"--agent", "agent-a", "--resume", "agent-a", "--resume=--continue", "--resume", "--notes={context_dir}",
		"--repo", "backend="+h.repo, "--repo", "frontend="+other); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newRepoCmd(), "update", "PAY-1", "frontend", "--agent", "agent-b", "--resume", "agent-b", "--resume", "resume"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newStartCmd(), "PAY-1", "--no-runtime"); err != nil {
		t.Fatal(err)
	}
	rt := &restartRuntime{}
	h.app.Runtime = rt
	if err := h.exec(t, h.app.newRestartCmd(), "PAY-1", "--root", "--resume"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newRestartCmd(), "PAY-1", "frontend", "--resume"); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"agent-a", "--continue", "--notes=" + h.app.Store.ContextDir("PAY-1")},
		{"agent-b", "resume"},
	}
	if len(rt.restarted) != 2 || !slices.Equal(rt.restarted[0].Command, want[0]) || !slices.Equal(rt.restarted[1].Command, want[1]) {
		t.Fatalf("restarted %+v", rt.restarted)
	}

	// A shell has nothing to resume.
	err := h.exec(t, h.app.newRestartCmd(), "PAY-1", "backend", "--resume")
	if err == nil || !strings.Contains(err.Error(), "there is no agent to resume") {
		t.Fatalf("restart --resume of a shell = %v", err)
	}
	// An agent without a resume command is told how to set one.
	if err := h.exec(t, h.app.newRepoCmd(), "update", "PAY-1", "backend", "--agent", "agent-c"); err != nil {
		t.Fatal(err)
	}
	err = h.exec(t, h.app.newRestartCmd(), "PAY-1", "backend", "--resume")
	if err == nil || !strings.Contains(err.Error(), "tspace repo update PAY-1 backend --resume") {
		t.Fatalf("restart --resume without one = %v", err)
	}
}

// A single-repo task's agent runs in the repository, and resumes there.
func TestRestartResumeOfTheAgentRepo(t *testing.T) {
	h := newHarness(t)
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-1", "--agent", "agent-a", "--repo", "backend="+h.repo); err != nil {
		t.Fatal(err)
	}
	// The agent repository's resume is the task's, as its agent is.
	if err := h.exec(t, h.app.newRepoCmd(), "update", "PAY-1", "backend", "--resume", "agent-a", "--resume=--continue"); err != nil {
		t.Fatal(err)
	}
	if got := h.load(t, "PAY-1").Resume; !slices.Equal(got, []string{"agent-a", "--continue"}) {
		t.Fatalf("task resume = %v", got)
	}
	if err := h.exec(t, h.app.newStartCmd(), "PAY-1", "--no-runtime"); err != nil {
		t.Fatal(err)
	}
	rt := &restartRuntime{}
	h.app.Runtime = rt
	if err := h.exec(t, h.app.newRestartCmd(), "PAY-1", "backend", "--resume"); err != nil {
		t.Fatal(err)
	}
	if w := rt.restarted[0]; w.Name != "backend" || !slices.Equal(w.Command, []string{"agent-a", "--continue"}) {
		t.Fatalf("restarted %+v", w)
	}
}

// The default resume command comes with the default agent and only with it:
// another agent's resume would run a different program.
func TestConfigResumeGoesWithTheConfigAgent(t *testing.T) {
	h := newHarness(t)
	if err := h.exec(t, h.app.newConfigCmd(), "agent", "agent-a", "--flag"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newConfigCmd(), "resume", "agent-a", "--continue"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-1"); err != nil {
		t.Fatal(err)
	}
	if got := h.load(t, "PAY-1").Resume; !slices.Equal(got, []string{"agent-a", "--continue"}) {
		t.Fatalf("default agent's task resume = %v", got)
	}
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-2", "--agent", "agent-b"); err != nil {
		t.Fatal(err)
	}
	if got := h.load(t, "PAY-2").Resume; len(got) != 0 {
		t.Fatalf("another agent got the default's resume: %v", got)
	}
}

// Changing the agent drops the resume command that went with the old one.
func TestTaskUpdateAgentDropsTheOldResume(t *testing.T) {
	h := newHarness(t)
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-1", "--agent", "agent-a", "--resume", "agent-a", "--resume", "-c"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newTaskCmd(), "update", "PAY-1", "--agent", "agent-b"); err != nil {
		t.Fatal(err)
	}
	if got := h.load(t, "PAY-1").Resume; len(got) != 0 || !strings.Contains(h.errb.String(), "went with the old agent") {
		t.Fatalf("resume = %v, stderr %q", got, h.errb.String())
	}
	if err := h.exec(t, h.app.newTaskCmd(), "update", "PAY-1", "--resume", "agent-b", "--resume", "again"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newTaskCmd(), "update", "PAY-1", "--no-resume"); err != nil {
		t.Fatal(err)
	}
	if got := h.load(t, "PAY-1").Resume; len(got) != 0 {
		t.Fatalf("--no-resume left %v", got)
	}
	// A shell has nothing to resume.
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-2", "--shell", "--resume", "x"); err == nil {
		t.Fatal("--resume accepted for a shell")
	}
}

func TestResumeIsKeptInProjects(t *testing.T) {
	tk := task.New("PAY-1")
	tk.Agent, tk.Resume = []string{"a"}, []string{"a", "-c"}
	tk.SetRepo("backend", task.Repo{Path: "/src/backend", Agent: []string{"b"}, Resume: []string{"b", "--last"}})
	p := task.ProjectFromTask("recipe", tk)
	if !slices.Equal(p.Resume, tk.Resume) || !slices.Equal(p.Repos["backend"].Resume, []string{"b", "--last"}) {
		t.Fatalf("project: %+v", p)
	}
}

func TestResumeReviewCases(t *testing.T) {
	h := newHarness(t)

	// A new default agent forgets the old one's resume command.
	if err := h.exec(t, h.app.newConfigCmd(), "agent", "agent-a"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newConfigCmd(), "resume", "agent-a", "-c"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newConfigCmd(), "agent", "agent-a"); err != nil {
		t.Fatal(err)
	}
	if c, _ := h.app.Store.LoadConfig(); len(c.Resume) == 0 {
		t.Fatal("re-setting the same agent dropped its resume")
	}
	if err := h.exec(t, h.app.newConfigCmd(), "agent", "agent-b"); err != nil {
		t.Fatal(err)
	}
	if c, _ := h.app.Store.LoadConfig(); len(c.Resume) != 0 {
		t.Fatalf("new default agent kept %v", c.Resume)
	}

	// Re-setting a task's agent to the same command keeps its resume.
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-1", "--agent", "agent-a", "--resume", "agent-a", "--resume", "-c",
		"--repo", "backend="+h.repo, "--with-root"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newTaskCmd(), "update", "PAY-1", "--agent", "agent-a"); err != nil {
		t.Fatal(err)
	}
	if got := h.load(t, "PAY-1").Resume; len(got) == 0 {
		t.Fatal("same agent dropped the resume")
	}

	// repo add validates --resume like every other command.
	if err := h.exec(t, h.app.newRepoCmd(), "add", "PAY-1", mkRepo(t), "--name", "front", "--agent", "x", "--resume", ""); err == nil {
		t.Fatal("empty --resume accepted")
	}

	// A shell window has nothing to resume, and says so without a bad hint.
	if err := h.exec(t, h.app.newStartCmd(), "PAY-1", "--no-runtime"); err != nil {
		t.Fatal(err)
	}
	h.app.Runtime = &restartRuntime{}
	err := h.exec(t, h.app.newRestartCmd(), "PAY-1", "backend", "--resume")
	if err == nil || !strings.Contains(err.Error(), "runs a shell; there is no agent to resume") {
		t.Fatalf("restart --resume of a shell = %v", err)
	}
}

func TestRemovingTheAgentRepoWarnsAboutResume(t *testing.T) {
	h := newHarness(t)
	other := mkRepo(t)
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-1", "--agent", "agent-a", "--resume", "agent-a", "--resume", "-c", "--repo", "backend="+h.repo); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newRepoCmd(), "add", "PAY-1", other, "--name", "frontend"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newRepoCmd(), "rm", "PAY-1", "backend"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.errb.String(), "will not find the one from backend") {
		t.Fatalf("stderr: %s", h.errb.String())
	}
}
