package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tsexec "github.com/wuyuz99/taskspace/internal/exec"
	tsgit "github.com/wuyuz99/taskspace/internal/git"
	"github.com/wuyuz99/taskspace/internal/task"
)

func renameGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	r, err := tsexec.Real().Run(t.Context(), dir, append([]string{"git"}, args...)...)
	if err != nil {
		t.Fatal(err)
	}
	return r.Out()
}

func renameFixture(t *testing.T) (*fixture, *task.Task) {
	t.Helper()
	f := newFixture(t)
	tk := f.task(t, "OLD", map[string]task.Repo{"a": {Path: gitRepo(t)}, "b": {Path: gitRepo(t)}})
	if _, err := f.svc.Start(t.Context(), tk.Name, StartOptions{NoRuntime: true}); err != nil {
		t.Fatal(err)
	}
	return f, tk
}

func TestRenamePreservesWorkspace(t *testing.T) {
	for _, branches := range []bool{false, true} {
		t.Run(map[bool]string{false: "keep-branches", true: "rename-branches"}[branches], func(t *testing.T) {
			f, tk := renameFixture(t)
			if err := os.MkdirAll(f.svc.Store.ContextDir("OLD"), 0o755); err != nil {
				t.Fatal(err)
			}
			tk.Title, tk.Ticket, tk.AgentRepo, tk.RootWindow = "title", "PAY-1", "a", true
			tk.Resume = []string{"agent", "--continue"}
			tk.Windows = []task.Window{{Name: "logs", Repo: "b", Command: []string{"tail", "-f", "log"}}}
			if err := f.svc.Store.Save(tk); err != nil {
				t.Fatal(err)
			}
			for _, p := range []string{filepath.Join(tk.Repos["a"].Worktree, "README.md"), filepath.Join(tk.Repos["b"].Worktree, ".env"), filepath.Join(f.svc.Store.ContextDir("OLD"), "TASK.md")} {
				if err := os.WriteFile(p, []byte("unfinished\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.svc.Store.MarkLaunched("OLD", "a"); err != nil {
				t.Fatal(err)
			}
			renameGit(t, tk.Repos["a"].Path, "branch", "--set-upstream-to=main", "agent/OLD")
			if err := f.svc.Rename(t.Context(), "OLD", "NEW", RenameOptions{Branches: branches}); err != nil {
				t.Fatal(err)
			}
			got, err := f.svc.Store.Load("NEW")
			if err != nil {
				t.Fatal(err)
			}
			if f.svc.Store.Exists("OLD") {
				t.Fatal("old record remains")
			}
			want := *tk
			want.Name = "NEW"
			want.Repos = map[string]task.Repo{}
			for name, r := range tk.Repos {
				r.Worktree = f.svc.Store.WorktreePath("NEW", name)
				if branches {
					r.Branch = "agent/NEW"
				}
				want.Repos[name] = r
			}
			if !reflect.DeepEqual(got, &want) {
				t.Fatalf("record = %+v; want %+v", got, &want)
			}
			for _, p := range []string{filepath.Join(got.Repos["a"].Worktree, "README.md"), filepath.Join(got.Repos["b"].Worktree, ".env"), filepath.Join(f.svc.Store.ContextDir("NEW"), "TASK.md")} {
				if b, err := os.ReadFile(p); err != nil || string(b) != "unfinished\n" {
					t.Fatalf("file %s: %q, %v", p, b, err)
				}
			}
			for _, r := range got.Repos {
				if branch := renameGit(t, r.Worktree, "branch", "--show-current"); branch != r.Branch {
					t.Fatalf("branch %s != %s", branch, r.Branch)
				}
			}
			if upstream := renameGit(t, got.Repos["a"].Worktree, "rev-parse", "--abbrev-ref", "@{upstream}"); upstream != "main" {
				t.Fatalf("upstream %q", upstream)
			}
			if !f.svc.Store.Launched("NEW", "a") || f.svc.Store.Launched("OLD", "a") {
				t.Fatal("launch metadata not moved")
			}
			warnings := eventsOf[Warning](f.events)
			found := false
			for _, w := range warnings {
				if strings.Contains(w.Text, tk.Repos["a"].Worktree) && strings.Contains(w.Text, "conversation") {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing resume warning: %+v", warnings)
			}
			if _, err := os.Stat(f.svc.Store.TaskDir("OLD")); !os.IsNotExist(err) {
				t.Fatalf("old root remains: %v", err)
			}
			if _, err := f.svc.Start(t.Context(), "NEW", StartOptions{NoRuntime: true}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRenameRefusals(t *testing.T) {
	for _, kind := range []string{"running", "runtime-error", "taken-name", "taken-dir", "taken-run", "locked-worktree", "branch-collision", "inside", "unclaimed", "shared-branch", "shared-base"} {
		t.Run(kind, func(t *testing.T) {
			f, tk := renameFixture(t)
			branches := false
			switch kind {
			case "running":
				f.rt.exists = true
			case "runtime-error":
				f.rt.hasErr = errors.New("cannot inspect session")
			case "taken-name":
				if _, err := f.svc.Store.Create("NEW"); err != nil {
					t.Fatal(err)
				}
			case "taken-dir":
				if err := os.MkdirAll(f.svc.Store.TaskDir("NEW"), 0o755); err != nil {
					t.Fatal(err)
				}
			case "taken-run":
				if err := f.svc.Store.MarkLaunched("NEW", "a"); err != nil {
					t.Fatal(err)
				}
			case "locked-worktree":
				renameGit(t, tk.Repos["b"].Path, "worktree", "lock", tk.Repos["b"].Worktree)
			case "branch-collision":
				branches = true
				renameGit(t, tk.Repos["b"].Path, "branch", "agent/NEW")
			case "inside":
				t.Chdir(tk.Repos["a"].Worktree)
			case "unclaimed":
				renameGit(t, tk.Repos["a"].Path, "worktree", "add", "-b", "orphan", filepath.Join(f.svc.Store.TaskDir("OLD"), "orphan"))
			case "shared-branch", "shared-base":
				branches = true
				other := task.New("OTHER")
				claim := tk.Repos["a"]
				if kind == "shared-base" {
					claim.Base, claim.Branch = claim.Branch, "feature"
				}
				other.SetRepo("a", claim)
				if err := f.svc.Store.CreatePrepared(other); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.svc.Rename(t.Context(), "OLD", "NEW", RenameOptions{Branches: branches}); err == nil {
				t.Fatal("rename accepted")
			}
			got, err := f.svc.Store.Load("OLD")
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tk) {
				t.Fatal("record changed on refusal")
			}
			for _, r := range tk.Repos {
				if branch := renameGit(t, r.Worktree, "branch", "--show-current"); branch != r.Branch {
					t.Fatal("worktree changed")
				}
			}
		})
	}
}

type renameFailRunner struct {
	calls  int
	fail   int
	cancel context.CancelFunc
	after  func()
}

func (r *renameFailRunner) Run(ctx context.Context, dir string, argv ...string) (tsexec.Result, error) {
	if len(argv) > 2 && ((argv[1] == "worktree" && argv[2] == "move") || (argv[1] == "branch" && argv[2] == "-m")) {
		r.calls++
		if r.calls == r.fail {
			if r.cancel != nil {
				r.cancel()
			}
			return tsexec.Result{}, errors.New("injected move failure")
		}
	}
	result, err := tsexec.Real().Run(ctx, dir, argv...)
	if err == nil && r.after != nil && len(argv) > 2 && argv[1] == "worktree" && argv[2] == "move" {
		r.after()
	}
	return result, err
}

func TestRenameRollsBack(t *testing.T) {
	for _, fail := range []int{2, 3, 4} {
		t.Run(string(rune('0'+fail)), func(t *testing.T) {
			f, tk := renameFixture(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			f.svc.Git = tsgit.New(&renameFailRunner{fail: fail, cancel: cancel})
			err := f.svc.Rename(ctx, "OLD", "NEW", RenameOptions{Branches: true})
			if err == nil || !strings.Contains(err.Error(), "injected") || strings.Contains(err.Error(), "rollback:") {
				t.Fatalf("rename = %v", err)
			}
			got, err := f.svc.Store.Load("OLD")
			if err != nil || !reflect.DeepEqual(got, tk) {
				t.Fatalf("old record = %+v, %v", got, err)
			}
			if f.svc.Store.Exists("NEW") {
				t.Fatal("new record remains")
			}
			if _, err := os.Stat(f.svc.Store.TaskDir("NEW")); !os.IsNotExist(err) {
				t.Fatalf("new root remains: %v", err)
			}
			for _, r := range tk.Repos {
				if branch := renameGit(t, r.Worktree, "branch", "--show-current"); branch != r.Branch {
					t.Fatalf("rollback branch = %s", branch)
				}
			}
		})
	}
}

func TestRenameUnstartedAndCustomBranches(t *testing.T) {
	for _, kind := range []string{"empty", "unstarted", "custom", "published"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture(t)
			repos := map[string]task.Repo{}
			if kind != "empty" {
				repos["a"] = task.Repo{Path: gitRepo(t)}
			}
			tk := f.task(t, "OLD", repos)
			if kind == "custom" {
				r := tk.Repos["a"]
				r.Branch = "feature"
				tk.Repos["a"] = r
				if err := f.svc.Store.Save(tk); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "published" {
				if _, err := f.svc.Start(t.Context(), "OLD", StartOptions{NoRuntime: true}); err != nil {
					t.Fatal(err)
				}
				remote := filepath.Join(t.TempDir(), "remote.git")
				renameGit(t, tk.Repos["a"].Path, "init", "--bare", remote)
				renameGit(t, tk.Repos["a"].Path, "remote", "add", "origin", remote)
				renameGit(t, tk.Repos["a"].Path, "push", "-u", "origin", "agent/OLD")
			}
			if err := f.svc.Rename(t.Context(), "OLD", "NEW", RenameOptions{Branches: true}); err != nil {
				t.Fatal(err)
			}
			got, err := f.svc.Store.Load("NEW")
			if err != nil {
				t.Fatal(err)
			}
			if kind != "empty" {
				want := tk.Repos["a"].Branch
				if kind == "unstarted" {
					want = "agent/NEW"
				}
				if got.Repos["a"].Branch != want {
					t.Fatalf("branch = %s", got.Repos["a"].Branch)
				}
			}
			if (kind == "custom" || kind == "published") && len(eventsOf[Warning](f.events)) == 0 {
				t.Fatal("branch kept without explanation")
			}
		})
	}
}

func TestRenameRecordFailureRestoresNotesAndRunData(t *testing.T) {
	f, tk := renameFixture(t)
	notes := filepath.Join(f.svc.Store.ContextDir("OLD"), "HANDOFF.md")
	if err := os.MkdirAll(filepath.Dir(notes), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(notes, []byte("handoff"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Store.MarkLaunched("OLD", "a"); err != nil {
		t.Fatal(err)
	}
	runner := &renameFailRunner{}
	runner.after = func() {
		runner.after = nil
		// Simulate a writer that does not honor task locks, after preflight.
		if _, err := f.svc.Store.Create("NEW"); err != nil {
			t.Fatal(err)
		}
	}
	f.svc.Git = tsgit.New(runner)
	if err := f.svc.Rename(t.Context(), "OLD", "NEW", RenameOptions{Branches: true}); err == nil || !errors.Is(err, task.ErrExists) {
		t.Fatalf("rename = %v", err)
	}
	got, err := f.svc.Store.Load("OLD")
	if err != nil || !reflect.DeepEqual(got, tk) {
		t.Fatalf("record = %+v, %v", got, err)
	}
	if b, err := os.ReadFile(notes); err != nil || string(b) != "handoff" {
		t.Fatalf("notes = %q, %v", b, err)
	}
	if !f.svc.Store.Launched("OLD", "a") || f.svc.Store.Launched("NEW", "a") {
		t.Fatal("run metadata not rolled back")
	}
	for _, r := range tk.Repos {
		if branch := renameGit(t, r.Worktree, "branch", "--show-current"); branch != r.Branch {
			t.Fatal("branch not restored")
		}
	}
}

func TestRenameConversationWarnings(t *testing.T) {
	for _, kind := range []string{"root", "repo", "agent-repo", "never-launched", "no-resume", "shell"} {
		t.Run(kind, func(t *testing.T) {
			f, tk := renameFixture(t)
			window := task.RootWindowName
			tk.Resume = []string{"agent", "--continue"}
			switch kind {
			case "repo":
				window = "b"
				r := tk.Repos[window]
				r.Agent = []string{"repo-agent"}
				r.Resume = []string{"repo-agent", "--resume"}
				tk.Repos[window] = r
			case "agent-repo":
				window = "a"
				tk.AgentRepo = window
			case "no-resume":
				tk.Resume = nil
			case "shell":
				tk.Agent = nil
			}
			if err := os.MkdirAll(f.svc.Store.ContextDir("OLD"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := f.svc.Store.Save(tk); err != nil {
				t.Fatal(err)
			}
			if kind != "never-launched" {
				if err := f.svc.Store.MarkLaunched("OLD", window); err != nil {
					t.Fatal(err)
				}
			}
			f.events = nil
			if err := f.svc.Rename(t.Context(), "OLD", "NEW", RenameOptions{}); err != nil {
				t.Fatal(err)
			}
			warnings := eventsOf[Warning](f.events)
			want := kind == "root" || kind == "repo" || kind == "agent-repo"
			if (len(warnings) > 0) != want {
				t.Fatalf("warnings = %+v", warnings)
			}
			if want {
				dir := f.svc.Store.TaskDir("OLD")
				if window != task.RootWindowName {
					dir = tk.Repos[window].Worktree
				}
				if !strings.Contains(warnings[0].Text, dir) {
					t.Fatalf("missing directory: %+v", warnings)
				}
			}
		})
	}
}

func TestRenameCustomPrefix(t *testing.T) {
	f := newFixture(t)
	tk := f.task(t, "OLD", map[string]task.Repo{"a": {Path: gitRepo(t)}})
	cfg, err := f.svc.Store.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	prefix := "work/"
	cfg.BranchPrefix = &prefix
	if err := f.svc.Store.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	r := tk.Repos["a"]
	r.Branch = "work/OLD"
	tk.Repos["a"] = r
	if err := f.svc.Store.Save(tk); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Start(t.Context(), "OLD", StartOptions{NoRuntime: true}); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Rename(t.Context(), "OLD", "NEW", RenameOptions{Branches: true}); err != nil {
		t.Fatal(err)
	}
	got, err := f.svc.Store.Load("NEW")
	if err != nil {
		t.Fatal(err)
	}
	if got.Repos["a"].Branch != "work/NEW" {
		t.Fatalf("branch = %s", got.Repos["a"].Branch)
	}
}

func TestRenameInvalidNamesAndMissingTask(t *testing.T) {
	f := newFixture(t)
	for _, pair := range [][2]string{{"../bad", "NEW"}, {"OLD", "../bad"}, {"OLD", "OLD"}, {"missing", "NEW"}} {
		if err := f.svc.Rename(t.Context(), pair[0], pair[1], RenameOptions{}); err == nil {
			t.Fatalf("accepted %v", pair)
		}
	}
}

func TestRenameRefusesStaleRegistrationAndExternalBranch(t *testing.T) {
	for _, external := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing", true: "external"}[external], func(t *testing.T) {
			f, tk := renameFixture(t)
			r := tk.Repos["a"]
			if external {
				renameGit(t, r.Worktree, "switch", "-c", "other")
				renameGit(t, r.Path, "worktree", "add", filepath.Join(t.TempDir(), "external"), r.Branch)
			} else {
				// Move the directory without informing git, leaving stale registration.
				if err := os.Rename(r.Worktree, filepath.Join(t.TempDir(), "missing")); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.svc.Rename(t.Context(), "OLD", "NEW", RenameOptions{Branches: true}); err == nil {
				t.Fatal("rename accepted")
			}
			if !f.svc.Store.Exists("OLD") || f.svc.Store.Exists("NEW") {
				t.Fatal("records changed")
			}
		})
	}
}
