package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wuyuz99/taskspace/internal/task"
)

// linkedWorktree adds a worktree of repo on a new branch, outside tspace.
func linkedWorktree(t *testing.T, repo, branch string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), strings.ReplaceAll(branch, "/", "-"))
	gitIn(t, repo, "worktree", "add", "-q", "-b", branch, dir)
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		dir = r
	}
	return dir
}

func TestImportAdoptsAWorktree(t *testing.T) {
	h := newHarness(t)
	gitIdentity(t)
	wt := linkedWorktree(t, h.repo, "fix-login")
	commitFile(t, wt, "fix.txt", "fix\n", "the fix")
	if err := os.WriteFile(filepath.Join(wt, ".env"), []byte("SECRET=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := h.exec(t, h.app.newImportCmd(), wt); err != nil {
		t.Fatalf("import: %v\n%s", err, h.errb.String())
	}
	tk := h.load(t, "fix-login")
	if len(tk.Repos) != 1 {
		t.Fatalf("repos %+v", tk.Repos)
	}
	name := tk.RepoNames()[0]
	r := tk.Repos[name]
	if r.Branch != "fix-login" || !samePath(r.Path, h.repo) || r.Base == "" {
		t.Fatalf("recorded %+v", r)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatalf("the old place is still there: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(r.Worktree, ".env")); err != nil || string(b) != "SECRET=1\n" {
		t.Fatalf("untracked file not carried over: %q, %v", b, err)
	}
	if got := gitIn(t, r.Worktree, "branch", "--show-current"); got != "fix-login" {
		t.Fatalf("moved worktree on %q", got)
	}
	if !strings.Contains(gitIn(t, h.repo, "worktree", "list"), filepath.ToSlash(filepath.Base(r.Worktree))) {
		t.Fatal("git does not know the worktree's new place")
	}
	// A single-repository task runs its agent in the worktree.
	if tk.AgentRepo != name {
		t.Errorf("agent repo %q", tk.AgentRepo)
	}
	// start reuses it as it is.
	if err := h.exec(t, h.app.newStartCmd(), "fix-login", "--no-runtime"); err != nil || !strings.Contains(h.out.String(), "worktree ready") {
		t.Fatalf("start after import = %v\n%s", err, h.out.String())
	}

	// Into an existing task, beside another repository.
	other := mkRepo(t)
	wt2 := linkedWorktree(t, other, "fix-api")
	if err := h.exec(t, h.app.newImportCmd(), wt2, "--task", "fix-login", "--name", "api"); err != nil {
		t.Fatal(err)
	}
	tk = h.load(t, "fix-login")
	if _, ok := tk.Repos["api"]; !ok || !tk.HasRootWindow() {
		t.Fatalf("second import: %+v", tk)
	}
}

func TestImportRefuses(t *testing.T) {
	h := newHarness(t)
	gitIdentity(t)

	// The main checkout cannot move.
	if err := h.exec(t, h.app.newImportCmd(), h.repo); err == nil || !strings.Contains(err.Error(), "main checkout") {
		t.Fatalf("import of the main checkout = %v", err)
	}
	// Uncommitted changes to tracked files.
	dirty := linkedWorktree(t, h.repo, "dirty")
	if err := os.WriteFile(filepath.Join(dirty, "README.md"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newImportCmd(), dirty); err == nil || !strings.Contains(err.Error(), "uncommitted") {
		t.Fatalf("import of a dirty worktree = %v", err)
	}
	// A detached HEAD.
	detached := filepath.Join(t.TempDir(), "detached")
	gitIn(t, h.repo, "worktree", "add", "-q", "--detach", detached)
	if err := h.exec(t, h.app.newImportCmd(), detached); err == nil || !strings.Contains(err.Error(), "detached") {
		t.Fatalf("import of a detached worktree = %v", err)
	}
	for _, name := range []string{"dirty", "detached"} {
		if h.app.Store.Exists(name) {
			t.Errorf("a refused import left task %s", name)
		}
	}

	// A failure once the task exists does not leave a new one behind.
	wt := linkedWorktree(t, h.repo, "blocked")
	dest := h.app.Store.WorktreePath("blocked", filepath.Base(h.repo))
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newImportCmd(), wt); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("import onto an existing directory = %v", err)
	}
	if h.app.Store.Exists("blocked") {
		t.Error("the failed import left its new task")
	}
	if _, err := os.Stat(wt); err != nil {
		t.Errorf("the worktree moved anyway: %v", err)
	}
}

// The cases the review found could misplace work or a task.
func TestImportSafety(t *testing.T) {
	h := newHarness(t)
	gitIdentity(t)
	withRemote(t, h.repo)

	// A branch pushed with -u tracks its own remote copy, which is not its
	// base: a rebase from there would drop the pushed commits.
	pushed := linkedWorktree(t, h.repo, "pushed")
	commitFile(t, pushed, "p.txt", "p\n", "pushed work")
	gitIn(t, pushed, "push", "-q", "-u", "origin", "pushed")
	if err := h.exec(t, h.app.newImportCmd(), pushed); err != nil {
		t.Fatal(err)
	}
	r := h.load(t, "pushed").Repos[filepath.Base(h.repo)]
	if r.Base == "origin/pushed" || r.Base == "" {
		t.Fatalf("base %q", r.Base)
	}

	// Another task's worktree is that task's.
	if err := h.exec(t, h.app.newImportCmd(), r.Worktree, "--task", "other"); err == nil || !strings.Contains(err.Error(), "pushed's "+filepath.Base(h.repo)) {
		t.Fatalf("import of a task's worktree = %v", err)
	}
	// A task of the default name is not joined without --task.
	again := linkedWorktree(t, mkRepo(t), "pushed")
	if err := h.exec(t, h.app.newImportCmd(), again); err == nil || !strings.Contains(err.Error(), "--task pushed") {
		t.Fatalf("import into a same-named task = %v", err)
	}
	// Not from inside the worktree it moves.
	inside := linkedWorktree(t, h.repo, "inside")
	t.Chdir(inside)
	if err := h.exec(t, h.app.newImportCmd(), "."); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("import from inside = %v", err)
	}
}

func TestImportOfABareRepositorysWorktree(t *testing.T) {
	h := newHarness(t)
	bare := filepath.Join(t.TempDir(), "x.git")
	gitIn(t, h.repo, "clone", "-q", "--bare", h.repo, bare)
	wt := filepath.Join(t.TempDir(), "feat")
	gitIn(t, bare, "worktree", "add", "-q", "-b", "feat", wt)
	if err := h.exec(t, h.app.newImportCmd(), wt); err == nil || !strings.Contains(err.Error(), "bare repository") {
		t.Fatalf("import from a bare repository = %v", err)
	}
	if h.app.Store.Exists("feat") {
		t.Fatal("left a task behind")
	}
}

func TestImportRefusesALockOrANameInUse(t *testing.T) {
	h := newHarness(t)
	gitIdentity(t)

	locked := linkedWorktree(t, h.repo, "locked")
	gitIn(t, h.repo, "worktree", "lock", "--reason", "in use", locked)
	if err := h.exec(t, h.app.newImportCmd(), locked); err == nil || !strings.Contains(err.Error(), "is locked (in use)") {
		t.Fatalf("import of a locked worktree = %v", err)
	}
	if _, err := os.Stat(locked); err != nil || h.app.Store.Exists("locked") {
		t.Fatalf("refused import moved the worktree (%v) or left a task", err)
	}

	// Joining a task whose repository name is taken leaves the task as it was.
	first := linkedWorktree(t, h.repo, "first")
	if err := h.exec(t, h.app.newImportCmd(), first, "--task", "joined"); err != nil {
		t.Fatal(err)
	}
	second := linkedWorktree(t, mkRepo(t), "second")
	err := h.exec(t, h.app.newImportCmd(), second, "--task", "joined", "--name", filepath.Base(h.repo))
	if err == nil || !strings.Contains(err.Error(), "already has a repo named") {
		t.Fatalf("import over a taken name = %v", err)
	}
	if tk := h.load(t, "joined"); len(tk.Repos) != 1 {
		t.Fatalf("joined task changed: %+v", tk.Repos)
	}
	if _, err := os.Stat(second); err != nil {
		t.Fatalf("refused import moved the worktree: %v", err)
	}
}

func TestImportAfterRepoRmKeepWorktree(t *testing.T) {
	for _, target := range []string{"original", "other"} {
		t.Run(target, func(t *testing.T) {
			h := newHarness(t)
			tk := h.startedTask(t, "original")
			name := tk.RepoNames()[0]
			original := tk.Repos[name]
			if err := os.WriteFile(filepath.Join(original.Worktree, ".env"), []byte("keep me"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := h.exec(t, h.app.newRepoCmd(), "rm", tk.Name, name, "--keep-worktree"); err != nil {
				t.Fatal(err)
			}
			if err := h.exec(t, h.app.newImportCmd(), original.Worktree, "--task", target, "--name", name); err != nil {
				t.Fatal(err)
			}
			got := h.load(t, target).Repos[name]
			if got.Branch != original.Branch || !samePath(got.Path, original.Path) {
				t.Fatalf("imported %+v, originally %+v", got, original)
			}
			if b, err := os.ReadFile(filepath.Join(got.Worktree, ".env")); err != nil || string(b) != "keep me" {
				t.Fatalf("preserved file = %q, %v", b, err)
			}
			if branch := gitIn(t, got.Worktree, "branch", "--show-current"); branch != original.Branch {
				t.Fatalf("branch = %q", branch)
			}
			if target != tk.Name {
				if len(h.load(t, tk.Name).Repos) != 0 {
					t.Fatal("old task claims a repository")
				}
				if _, err := os.Stat(original.Worktree); !os.IsNotExist(err) {
					t.Fatalf("old worktree still exists: %v", err)
				}
			}
		})
	}
}

func TestImportChecksOwnershipOutsideWorktreesDir(t *testing.T) {
	h := newHarness(t)
	wt := linkedWorktree(t, h.repo, "external")
	owner, err := h.app.Store.Create("owner")
	if err != nil {
		t.Fatal(err)
	}
	owner.SetRepo("backend", task.Repo{Path: h.repo, Worktree: wt, Branch: "external"})
	if err := h.app.Store.Save(owner); err != nil {
		t.Fatal(err)
	}
	err = h.exec(t, h.app.newImportCmd(), wt, "--task", "other")
	if err == nil || !strings.Contains(err.Error(), "owner's backend") || !strings.Contains(err.Error(), "repo rm owner backend --keep-worktree") {
		t.Fatalf("claimed import = %v", err)
	}
	if h.app.Store.Exists("other") {
		t.Fatal("refusal created a task")
	}
	if _, err := os.Stat(wt); err != nil {
		t.Fatal(err)
	}
}

func TestImportRefusesUnreadableOwnership(t *testing.T) {
	h := newHarness(t)
	wt := linkedWorktree(t, h.repo, "unclaimed")
	if err := os.MkdirAll(h.app.Store.TasksDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.app.Store.TasksDir(), "broken.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := h.exec(t, h.app.newImportCmd(), wt)
	if err == nil || !strings.Contains(err.Error(), "check worktree ownership") {
		t.Fatalf("import = %v", err)
	}
	if h.app.Store.Exists("unclaimed") {
		t.Fatal("refusal created a task")
	}
	if _, err := os.Stat(wt); err != nil {
		t.Fatal(err)
	}
}
