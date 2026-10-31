package cli

import (
	"os"
	osexec "os/exec"
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

// Work in progress comes along as it is: nothing is committed or staged, and
// a failure takes it back the same way.
func TestImportKeepsUncommittedChanges(t *testing.T) {
	h := newHarness(t)
	gitIdentity(t)
	wt := linkedWorktree(t, h.repo, "wip")
	commitFile(t, wt, "staged.txt", "old\n", "a file to stage")
	if err := os.WriteFile(filepath.Join(wt, "README.md"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, "staged.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, wt, "add", "staged.txt")
	before := gitIn(t, wt, "status", "--porcelain")

	// Already attached to T, the repository is refused before the move.
	h.startedTask(t, "T")
	if err := h.exec(t, h.app.newImportCmd(), wt, "--task", "T", "--name", "second"); err == nil || !strings.Contains(err.Error(), "already attached") {
		t.Fatalf("import into a task with the repository = %v", err)
	}
	if got := gitIn(t, wt, "status", "--porcelain"); got != before {
		t.Fatalf("moved back as %q; want %q", got, before)
	}

	if err := h.exec(t, h.app.newImportCmd(), wt); err != nil {
		t.Fatalf("import: %v\n%s", err, h.errb.String())
	}
	if !strings.Contains(h.out.String(), "2 uncommitted changes kept; nothing was committed") {
		t.Fatalf("output: %s", h.out.String())
	}
	moved := h.load(t, "wip")
	if got := gitIn(t, moved.Repos[moved.RepoNames()[0]].Worktree, "status", "--porcelain"); got != before {
		t.Fatalf("after the move %q; want %q", got, before)
	}
}

// repoNamed makes a repository in a directory called name.
func repoNamed(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	gitIn(t, mkRepo(t), "clone", "-q", ".", dir)
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		dir = r
	}
	return dir
}

// Several worktrees get the layout task create gives them: nothing has run,
// so the agent is at the root rather than in whichever came first.
func TestImportSeveralIntoANewTask(t *testing.T) {
	h := newHarness(t)
	api := linkedWorktree(t, repoNamed(t, "api"), "fix-api")
	web := linkedWorktree(t, repoNamed(t, "web"), "fix-web")

	if err := h.exec(t, h.app.newImportCmd(), api, web, "--task", "both"); err != nil {
		t.Fatalf("import: %v\n%s", err, h.errb.String())
	}
	tk := h.load(t, "both")
	if len(tk.Repos) != 2 || tk.Repos["api"].Branch != "fix-api" || tk.Repos["web"].Branch != "fix-web" {
		t.Fatalf("repos %+v", tk.Repos)
	}
	if tk.AgentRepo != "" || !tk.HasRootWindow() {
		t.Fatalf("agent in %q, root window %v", tk.AgentRepo, tk.HasRootWindow())
	}
	for _, old := range []string{api, web} {
		if _, err := os.Stat(old); !os.IsNotExist(err) {
			t.Fatalf("%s is still there: %v", old, err)
		}
	}
	if out := h.out.String(); strings.Count(out, "moved ") != 2 || strings.Count(out, "start it with") != 1 {
		t.Fatalf("output: %s", out)
	}
}

func TestImportSeveralIntoATaskWithAnAgent(t *testing.T) {
	h := newHarness(t)
	if err := h.exec(t, h.app.newImportCmd(), linkedWorktree(t, h.repo, "single")); err != nil {
		t.Fatal(err)
	}
	agentRepo := h.load(t, "single").AgentRepo
	if agentRepo == "" {
		t.Fatal("one worktree: the agent is not in it")
	}
	api := linkedWorktree(t, repoNamed(t, "api"), "fix-api")
	web := linkedWorktree(t, repoNamed(t, "web"), "fix-web")
	if err := h.exec(t, h.app.newImportCmd(), api, web, "--task", "single"); err != nil {
		t.Fatal(err)
	}
	if tk := h.load(t, "single"); tk.AgentRepo != agentRepo || !tk.RootWindow || len(tk.Repos) != 3 {
		t.Fatalf("after joining: %+v", tk)
	}
}

func TestImportWithRoot(t *testing.T) {
	h := newHarness(t)
	if err := h.exec(t, h.app.newImportCmd(), linkedWorktree(t, h.repo, "rooted"), "--with-root"); err != nil {
		t.Fatal(err)
	}
	if tk := h.load(t, "rooted"); tk.AgentRepo != "" || !tk.RootWindow {
		t.Fatalf("task %+v", tk)
	}
	again := linkedWorktree(t, mkRepo(t), "again")
	err := h.exec(t, h.app.newImportCmd(), again, "--task", "rooted", "--with-root")
	if err == nil || !strings.Contains(err.Error(), "--with-root is for a new task") {
		t.Fatalf("--with-root into an existing task = %v", err)
	}
	if _, err := os.Stat(again); err != nil {
		t.Fatal(err)
	}
}

// Nothing moves until every path is known to be good.
func TestImportSeveralRefusesBeforeMoving(t *testing.T) {
	h := newHarness(t)
	good := linkedWorktree(t, h.repo, "good")
	other := linkedWorktree(t, h.repo, "other")
	sameName := linkedWorktree(t, repoNamed(t, filepath.Base(h.repo)), "same-name")
	detached := filepath.Join(t.TempDir(), "detached")
	gitIn(t, mkRepo(t), "worktree", "add", "-q", "--detach", detached)
	lower := linkedWorktree(t, repoNamed(t, "api"), "lower")
	upper := linkedWorktree(t, repoNamed(t, "API"), "upper")
	taken := linkedWorktree(t, repoNamed(t, "taken"), "wt-taken")
	if err := os.MkdirAll(h.app.Store.WorktreePath("blocked", "taken"), 0o755); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		name string
		args []string
		want string
	}{
		{"no task", []string{good, sameName}, "--task"},
		{"one name for two", []string{good, sameName, "--task", "T", "--name", "x"}, "--name names one"},
		{"a bad path after a good one", []string{good, detached, "--task", "T"}, "detached"},
		{"the same repository twice", []string{good, other, "--task", "T"}, "both worktrees of"},
		{"the same path twice", []string{good, good, "--task", "T"}, "both worktrees of"},
		{"the same repository name", []string{good, sameName, "--task", "T"}, "would both be repository"},
		{"names differing in case", []string{lower, upper, "--task", "T"}, "would both be repository"},
		{"a place already taken", []string{good, taken, "--task", "blocked"}, "already exists"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if err := h.exec(t, h.app.newImportCmd(), c.args...); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("import = %v", err)
			}
			if _, err := os.Stat(good); err != nil {
				t.Fatalf("the good worktree moved: %v", err)
			}
			if h.app.Store.Exists("T") || h.app.Store.Exists("blocked") {
				t.Fatal("a refused import left its task")
			}
		})
	}
}

// A failure partway takes back what already moved. An upstream that no longer
// exists is found only when the repository is recorded, after its move.
func TestImportSeveralRollsBack(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, h *harness) (taskName string)
		repos int
	}{
		{"a new task", func(*testing.T, *harness) string { return "T" }, 0},
		{"an existing task", func(t *testing.T, h *harness) string { return h.startedTask(t, "T").Name }, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			taskName := c.setup(t, h)
			good := linkedWorktree(t, repoNamed(t, "a-good"), "good")
			if err := os.WriteFile(filepath.Join(good, ".env"), []byte("keep\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			stuck := linkedWorktree(t, repoNamed(t, "b-stuck"), "stuck")
			gitIn(t, stuck, "config", "branch.stuck.remote", "origin")
			gitIn(t, stuck, "config", "branch.stuck.merge", "refs/heads/gone")

			err := h.exec(t, h.app.newImportCmd(), good, stuck, "--task", taskName)
			if err == nil {
				t.Fatal("import of a worktree with no base succeeded")
			}
			if strings.Contains(err.Error(), "moved back") {
				t.Fatalf("rollback failed: %v", err)
			}
			for _, wt := range []string{good, stuck} {
				if _, err := os.Stat(wt); err != nil {
					t.Fatalf("not moved back: %v", err)
				}
			}
			if b, err := os.ReadFile(filepath.Join(good, ".env")); err != nil || string(b) != "keep\n" {
				t.Fatalf("the first worktree is not back: %q, %v", b, err)
			}
			if strings.Contains(h.out.String(), "added a-good") {
				t.Fatalf("reported a repository it took back:\n%s", h.out.String())
			}
			if c.repos == 0 {
				if h.app.Store.Exists(taskName) {
					t.Fatal("the failed import left its new task")
				}
				return
			}
			if tk := h.load(t, taskName); len(tk.Repos) != c.repos {
				t.Fatalf("task changed: %+v", tk.Repos)
			}
		})
	}
}

func TestImportRefuses(t *testing.T) {
	h := newHarness(t)
	gitIdentity(t)

	// The main checkout cannot move.
	if err := h.exec(t, h.app.newImportCmd(), h.repo); err == nil || !strings.Contains(err.Error(), "main checkout") {
		t.Fatalf("import of the main checkout = %v", err)
	}
	// Conflicted files, with no unfinished operation to find: a stash that
	// did not apply cleanly.
	conflicted := linkedWorktree(t, h.repo, "conflicted")
	if err := os.WriteFile(filepath.Join(conflicted, "README.md"), []byte("stashed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, conflicted, "stash")
	commitFile(t, conflicted, "README.md", "committed\n", "the other side")
	pop := osexec.Command("git", "stash", "pop")
	pop.Dir = conflicted
	if err := pop.Run(); err == nil {
		t.Fatal("stash pop did not conflict")
	}
	if err := h.exec(t, h.app.newImportCmd(), conflicted); err == nil || !strings.Contains(err.Error(), "1 conflicted file") {
		t.Fatalf("import of a conflicted worktree = %v", err)
	}
	// An unfinished merge, staged and resolved or not.
	merging := linkedWorktree(t, h.repo, "merging")
	commitFile(t, merging, "ours.txt", "ours\n", "ours")
	theirs := linkedWorktree(t, h.repo, "theirs-work")
	commitFile(t, theirs, "theirs.txt", "theirs\n", "theirs")
	gitIn(t, merging, "merge", "--no-commit", "--no-ff", "theirs-work")
	if err := h.exec(t, h.app.newImportCmd(), merging); err == nil || !strings.Contains(err.Error(), "unfinished merge") {
		t.Fatalf("import of an unfinished merge = %v", err)
	}
	// A detached HEAD.
	detached := filepath.Join(t.TempDir(), "detached")
	gitIn(t, h.repo, "worktree", "add", "-q", "--detach", detached)
	if err := h.exec(t, h.app.newImportCmd(), detached); err == nil || !strings.Contains(err.Error(), "detached") {
		t.Fatalf("import of a detached worktree = %v", err)
	}
	for _, name := range []string{"conflicted", "merging", "detached"} {
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
