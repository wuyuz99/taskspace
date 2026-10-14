package cli

import (
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := osexec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=tspace", "GIT_AUTHOR_EMAIL=t@example.invalid",
		"GIT_COMMITTER_NAME=tspace", "GIT_COMMITTER_EMAIL=t@example.invalid")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// withRemote gives repo a bare origin holding main, and a colleague's clone of
// it that has pushed branch feature/x and develop, neither of which repo has
// fetched.
func withRemote(t *testing.T, repo string) {
	t.Helper()
	remote := t.TempDir()
	gitIn(t, remote, "init", "-q", "--bare", "-b", "main", ".")
	gitIn(t, repo, "remote", "add", "origin", remote)
	gitIn(t, repo, "push", "-q", "-u", "origin", "main")
	gitIn(t, repo, "remote", "set-head", "origin", "-a")

	other := t.TempDir()
	gitIn(t, other, "clone", "-q", remote, ".")
	for _, b := range []string{"feature/x", "develop"} {
		gitIn(t, other, "checkout", "-q", "-b", b, "main")
		name := strings.ReplaceAll(b, "/", "-") + ".txt"
		if err := os.WriteFile(filepath.Join(other, name), []byte(b+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		gitIn(t, other, "add", name)
		gitIn(t, other, "commit", "-q", "-m", "work on "+b)
		gitIn(t, other, "push", "-q", "origin", b)
	}
}

func TestParseRepoSpec(t *testing.T) {
	cases := map[string]repoSpec{
		"backend":                     {Source: "backend"},
		"backend@feature/x":           {Source: "backend", Branch: "feature/x"},
		"backend:develop":             {Source: "backend", Base: "develop"},
		"backend@fix-1:origin/main":   {Source: "backend", Branch: "fix-1", Base: "origin/main"},
		"api=backend@fix-1":           {Name: "api", Source: "backend", Branch: "fix-1"},
		"api=~/src/backend@x":         {Name: "api", Source: "~/src/backend", Branch: "x"},
		"backend@user@feature":        {Source: "backend", Branch: "user@feature"},
		"backend@with=equals:develop": {Source: "backend", Branch: "with=equals", Base: "develop"},
	}
	for in, want := range cases {
		got, err := parseRepoSpec(in)
		if err != nil || got != want {
			t.Errorf("parseRepoSpec(%q) = %+v, %v; want %+v", in, got, err, want)
		}
	}
	for _, bad := range []string{"backend@", "backend:", "@x", "=backend", "api=", "backend@x:"} {
		if got, err := parseRepoSpec(bad); err == nil {
			t.Errorf("parseRepoSpec(%q) = %+v, want an error", bad, got)
		}
	}

	// A directory that exists as typed is a path, whatever it contains.
	dir := filepath.Join(t.TempDir(), "odd@name")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := parseRepoSpec(dir); err != nil || got.Source != dir || got.Branch != "" {
		t.Errorf("parseRepoSpec(%q) = %+v, %v; want the path itself", dir, got, err)
	}
}

func TestConfigRepoRegistry(t *testing.T) {
	h := newHarness(t)
	cfg := h.app.newConfigCmd
	if err := h.exec(t, cfg(), "repo", "backend", h.repo); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, cfg(), "repo", "backend"); err != nil || strings.TrimSpace(h.out.String()) != h.repo {
		t.Fatalf("lookup = %q, %v", h.out.String(), err)
	}
	if err := h.exec(t, cfg(), "repo", "api", h.repo); err == nil || !strings.Contains(err.Error(), `registered as "backend"`) {
		t.Fatalf("second name for one repository = %v, want a refusal", err)
	}
	if err := h.exec(t, cfg(), "repo", "web", t.TempDir()); err == nil {
		t.Fatal("registered a directory that is not a repository")
	}
	if err := h.exec(t, cfg()); err != nil || !strings.Contains(h.out.String(), "backend") {
		t.Fatalf("config = %q, %v", h.out.String(), err)
	}
	if err := h.exec(t, cfg(), "repo", "backend", "--unset"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-1", "backend"); err == nil || !strings.Contains(err.Error(), "tspace config repo") {
		t.Fatalf("create with an unregistered name = %v, want a hint", err)
	}
	if h.app.Store.Exists("PAY-1") {
		t.Fatal("failed create left a record")
	}
}

// The daily case: registered names, one existing branch that only the remote
// has, one new branch off a base that only the remote has.
func TestTaskCreateWithRegisteredReposAndBranches(t *testing.T) {
	h := newHarness(t)
	frontend, infra := mkRepo(t), mkRepo(t)
	withRemote(t, frontend)
	withRemote(t, infra)
	for name, path := range map[string]string{"backend": h.repo, "frontend": frontend, "infra": infra} {
		if err := h.exec(t, h.app.newConfigCmd(), "repo", name, path); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.exec(t, h.app.newConfigCmd(), "branch-prefix", "isaac/"); err != nil {
		t.Fatal(err)
	}

	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-7", "backend", "frontend@feature/x", "--repo", "infra:develop"); err != nil {
		t.Fatal(err)
	}
	out := h.out.String()
	for _, want := range []string{
		"isaac/PAY-7 (new, from main)",
		"feature/x (will track origin/feature/x)",
		"isaac/PAY-7 (new, from origin/develop)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("create output lacks %q:\n%s", want, out)
		}
	}
	tk, err := h.app.Store.Load("PAY-7")
	if err != nil {
		t.Fatal(err)
	}
	if r := tk.Repos["infra"]; r.Branch != "isaac/PAY-7" || r.Base != "origin/develop" || r.Path != infra {
		t.Errorf("infra = %+v", r)
	}

	if err := h.exec(t, h.app.newStartCmd(), "PAY-7", "--no-runtime"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.out.String(), "created worktree on feature/x (tracking origin/feature/x)") {
		t.Errorf("start output:\n%s", h.out.String())
	}
	if _, err := os.Stat(filepath.Join(tk.Repos["frontend"].Worktree, "feature-x.txt")); err != nil {
		t.Errorf("frontend worktree lacks the remote branch's work: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tk.Repos["infra"].Worktree, "develop.txt")); err != nil {
		t.Errorf("infra was not forked from origin/develop: %v", err)
	}
	// The new branch must be pushable as itself, not tracking its base.
	cmd := osexec.Command("git", "rev-parse", "--abbrev-ref", "isaac/PAY-7@{upstream}")
	cmd.Dir = infra
	if up, err := cmd.Output(); err == nil {
		t.Errorf("isaac/PAY-7 tracks %s", up)
	}
}

func TestRepoAddRefusesPartsGivenTwiceAndMissingBases(t *testing.T) {
	h := newHarness(t)
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-8"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newConfigCmd(), "repo", "backend", h.repo); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newRepoCmd(), "add", "PAY-8", "backend@x", "--branch", "y"); err == nil || !strings.Contains(err.Error(), "given twice") {
		t.Fatalf("branch in spec and flag = %v", err)
	}
	if err := h.exec(t, h.app.newRepoCmd(), "add", "PAY-8", "backend:nope"); err == nil || !strings.Contains(err.Error(), `base "nope"`) {
		t.Fatalf("missing base = %v", err)
	}
	if err := h.exec(t, h.app.newRepoCmd(), "add", "PAY-8", "backend@bad..name"); err == nil {
		t.Fatal("accepted an invalid branch name")
	}
	// A named branch nobody has is created, but said out loud.
	if err := h.exec(t, h.app.newRepoCmd(), "add", "PAY-8", "backend@feature/typo"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.errb.String(), "no branch feature/typo") {
		t.Errorf("no note about the missing branch: %q", h.errb.String())
	}
}
