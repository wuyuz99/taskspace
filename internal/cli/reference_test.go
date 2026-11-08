package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// commitTo adds a commit to dir's checked-out branch.
func commitTo(t *testing.T, dir, file string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(file+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "add", file)
	gitIn(t, dir, "commit", "-m", file)
}

func canWrite(path string) bool {
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return false
	}
	f.Close()
	return true
}

// refTask is a task working in h.repo with lib as a reference, started.
func refTask(t *testing.T, h *harness, name string) (lib string) {
	t.Helper()
	lib = mkRepo(t)
	if err := h.exec(t, h.app.newTaskCmd(), "create", name, h.repo, "--ref", "lib="+lib); err != nil {
		t.Fatalf("create: %v\n%s", err, h.errb)
	}
	if !strings.Contains(h.out.String(), "reference at main, read-only") {
		t.Fatalf("create said:\n%s", h.out)
	}
	if err := h.exec(t, h.app.newStartCmd(), name); err != nil {
		t.Fatalf("start: %v\n%s", err, h.errb)
	}
	return lib
}

// A reference is checked out at its base, detached and read-only, with no
// window: the root window, which a task with one gets, sees it.
func TestReferenceIsReadOnlyWithNoWindow(t *testing.T) {
	h := newHarness(t)
	refTask(t, h, "PAY-1")
	tk := h.load(t, "PAY-1")
	r := tk.Repos["lib"]
	if !r.Reference || r.Branch != "" || r.Base != "main" || tk.Version != 4 {
		t.Fatalf("record: %+v, version %d", r, tk.Version)
	}
	if tk.AgentRepo != "" || !tk.HasRootWindow() {
		t.Fatalf("a task with a reference has no root window: %+v", tk)
	}
	session := h.app.Runtime.SessionName("PAY-1")
	if _, ok := h.fake().Window(session, "lib"); ok {
		t.Fatal("the reference got a window")
	}
	if _, ok := h.fake().Window(session, "_root"); !ok {
		t.Fatal("no root window")
	}
	if !strings.Contains(h.out.String(), "created read-only reference at main") {
		t.Fatalf("start said:\n%s", h.out)
	}
	if head := gitIn(t, r.Worktree, "rev-parse", "--abbrev-ref", "HEAD"); head != "HEAD" {
		t.Fatalf("reference is on %s, not detached", head)
	}
	if canWrite(filepath.Join(r.Worktree, "README.md")) {
		t.Fatal("a reference's file is writable")
	}
	if gitIn(t, r.Worktree, "status", "--porcelain") != "" {
		t.Fatal("making files read-only shows as a change")
	}

	if err := h.exec(t, h.app.newStatusCmd(), "PAY-1"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(strings.Fields(h.out.String()), " "), "PAY-1 lib main - reference clean") {
		t.Fatalf("status:\n%s", h.out)
	}

	for _, args := range [][]string{{"PAY-1", "lib"}} {
		if err := h.exec(t, h.app.newStopCmd(), args...); err == nil || !strings.Contains(err.Error(), "is a reference, which has no window") {
			t.Fatalf("stop %v = %v", args, err)
		}
		if err := h.exec(t, h.app.newRestartCmd(), args...); err == nil || !strings.Contains(err.Error(), "is a reference, which has no window") {
			t.Fatalf("restart %v = %v", args, err)
		}
	}
	if err := h.exec(t, h.app.newRepoCmd(), "update", "PAY-1", "lib", "--agent", "claude"); err == nil || !strings.Contains(err.Error(), "make it a working repository first") {
		t.Fatalf("an agent for a reference = %v", err)
	}
}

// status counts what a reference is missing from its base, and repo update
// --base fetches and moves it there, read-only still.
func TestReferenceCatchesUpWithItsBase(t *testing.T) {
	h := newHarness(t)
	lib := refTask(t, h, "PAY-1")
	wt := h.load(t, "PAY-1").Repos["lib"].Worktree
	commitTo(t, lib, "new.go")

	if err := h.exec(t, h.app.newStatusCmd(), "PAY-1", "--json"); err != nil {
		t.Fatal(err)
	}
	var got []statusJSON
	if err := json.Unmarshal(h.out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	var ref *statusRepoJSON
	for i := range got[0].Repos {
		if got[0].Repos[i].Repo == "lib" {
			ref = &got[0].Repos[i]
		}
	}
	if ref == nil || !ref.Reference || ref.Base != "main" || ref.Branch != "" || ref.Behind != 1 || ref.Runtime != "no-window" || ref.Unpushed != 0 {
		t.Fatalf("status --json: %+v", ref)
	}

	if err := h.exec(t, h.app.newRepoCmd(), "update", "PAY-1", "lib", "--base", "main"); err != nil {
		t.Fatalf("update: %v\n%s", err, h.errb)
	}
	if !strings.Contains(h.out.String(), "base: main (checked out)") {
		t.Fatalf("update said:\n%s", h.out)
	}
	if _, err := os.Stat(filepath.Join(wt, "new.go")); err != nil {
		t.Fatal("the reference did not move to its base")
	}
	if canWrite(filepath.Join(wt, "new.go")) || canWrite(filepath.Join(wt, "README.md")) {
		t.Fatal("moving made the reference writable")
	}
	if err := h.exec(t, h.app.newStatusCmd(), "PAY-1"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(h.out.String(), "behind") {
		t.Fatalf("still behind:\n%s", h.out)
	}
	if err := h.exec(t, h.app.newRepoCmd(), "update", "PAY-1", "lib", "--base", "main", "--rebase"); err == nil || !strings.Contains(err.Error(), "no commits of its own") {
		t.Fatalf("--rebase on a reference = %v", err)
	}
}

// Clean removes a reference with no branch to look at, but not one that
// was changed despite its protection.
func TestCleanRemovesAReferenceUnlessChanged(t *testing.T) {
	h := newHarness(t)
	lib := refTask(t, h, "PAY-1")
	wt := h.load(t, "PAY-1").Repos["lib"].Worktree
	stray := filepath.Join(wt, "stray.txt")
	if err := os.WriteFile(stray, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := h.exec(t, h.app.newCleanCmd(), "PAY-1", "--delete-branch")
	if err == nil || !strings.Contains(err.Error(), "lib: 1 uncommitted change") {
		t.Fatalf("clean of a changed reference = %v", err)
	}
	if err := os.Remove(stray); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newCleanCmd(), "PAY-1", "--delete-branch"); err != nil {
		t.Fatalf("clean: %v\n%s", err, h.errb)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatalf("the reference is still there: %v", err)
	}
	if strings.Contains(h.out.String()+h.errb.String(), "lib          kept branch") {
		t.Fatalf("clean looked for a reference's branch:\n%s%s", h.out, h.errb)
	}
	if gitIn(t, lib, "worktree", "list", "--porcelain") == "" || strings.Contains(gitIn(t, lib, "worktree", "list"), wt) {
		t.Fatal("the reference's worktree is still registered")
	}
}

// --branch makes a reference a working repository where it stands, its
// files writable again.
func TestReferenceBecomesAWorkingRepository(t *testing.T) {
	h := newHarness(t)
	refTask(t, h, "PAY-1")
	wt := h.load(t, "PAY-1").Repos["lib"].Worktree
	if err := h.exec(t, h.app.newRepoCmd(), "update", "PAY-1", "lib", "--branch", "me/lib"); err != nil {
		t.Fatalf("update: %v\n%s", err, h.errb)
	}
	r := h.load(t, "PAY-1").Repos["lib"]
	if r.Reference || r.Branch != "me/lib" || r.Base != "main" {
		t.Fatalf("record: %+v", r)
	}
	if head := gitIn(t, wt, "rev-parse", "--abbrev-ref", "HEAD"); head != "me/lib" {
		t.Fatalf("worktree on %s", head)
	}
	if !canWrite(filepath.Join(wt, "README.md")) {
		t.Fatal("still read-only")
	}
	if !strings.Contains(h.out.String(), "tspace start PAY-1 lib") {
		t.Fatalf("update said:\n%s", h.out)
	}
	if v := h.load(t, "PAY-1").Version; v != 1 {
		t.Fatalf("a task without references is version %d", v)
	}
}

// A reference takes a base but nothing that belongs to a working repository.
func TestReferenceRefusesWhatItCannotHave(t *testing.T) {
	h := newHarness(t)
	if _, err := h.app.Store.Create("PAY-1"); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{h.repo + "@feature", "--ref"}, "has no branch of its own"},
		{[]string{h.repo, "--ref", "--agent", "claude"}, "so no agent"},
		{[]string{h.repo, "--ref", "--post-create", "make"}, "no post_create hook"},
		{[]string{h.repo + ":HEAD~0", "--ref"}, ""},
	} {
		err := h.exec(t, h.app.newRepoCmd(), append([]string{"add", "PAY-1"}, c.args...)...)
		if c.want == "" {
			if err == nil {
				t.Fatalf("add %v took a base that moves on its own", c.args)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("add %v = %v", c.args, err)
		}
	}
}

// The same base given again fetches first, so a reference catches up with
// what was pushed since.
func TestReferenceUpdateFetches(t *testing.T) {
	h := newHarness(t)
	lib := mkRepo(t)
	withRemote(t, lib)
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-1", h.repo, "--ref", "lib="+lib, "--start"); err != nil {
		t.Fatalf("create: %v\n%s", err, h.errb)
	}
	r := h.load(t, "PAY-1").Repos["lib"]
	if r.Base != "origin/main" {
		t.Fatalf("base %q", r.Base)
	}
	other := t.TempDir()
	gitIn(t, other, "clone", "-q", gitIn(t, lib, "remote", "get-url", "origin"), ".")
	commitTo(t, other, "pushed.go")
	gitIn(t, other, "push", "-q", "origin", "main")

	if err := h.exec(t, h.app.newRepoCmd(), "update", "PAY-1", "lib", "--base", "origin/main"); err != nil {
		t.Fatalf("update: %v\n%s", err, h.errb)
	}
	if _, err := os.Stat(filepath.Join(r.Worktree, "pushed.go")); err != nil {
		t.Fatal("the reference did not fetch what was pushed")
	}
}

// A reference left in place by repo rm --keep-worktree is the user's, so
// its files are writable again.
func TestKeptReferenceIsWritable(t *testing.T) {
	h := newHarness(t)
	refTask(t, h, "PAY-1")
	wt := h.load(t, "PAY-1").Repos["lib"].Worktree
	if err := h.exec(t, h.app.newRepoCmd(), "rm", "PAY-1", "lib", "--keep-worktree"); err != nil {
		t.Fatalf("rm: %v\n%s", err, h.errb)
	}
	if !canWrite(filepath.Join(wt, "README.md")) {
		t.Fatal("a kept reference is still read-only")
	}
}
