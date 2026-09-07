package task

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	return NewStore(t.TempDir())
}

func TestCreateAndLoadRoundTrip(t *testing.T) {
	s := newTestStore(t)

	created, err := s.Create("checkout-redesign")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	created.SetRepo("backend", Repo{
		Path:       "/src/backend",
		Branch:     DefaultBranch("checkout-redesign"),
		Base:       "origin/main",
		Worktree:   s.WorktreePath("checkout-redesign", "backend"),
		PostCreate: "cp ../../.env .",
		Agent:      []string{"claude"},
	})
	if err := s.Save(created); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := s.Load("checkout-redesign")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Name != "checkout-redesign" {
		t.Errorf("Name = %q, want checkout-redesign", got.Name)
	}
	be, ok := got.Repo("backend")
	if !ok {
		t.Fatal("backend repo missing after round trip")
	}
	if be.Base != "origin/main" {
		t.Errorf("Base = %q, want origin/main", be.Base)
	}
	if be.PostCreate != "cp ../../.env ." {
		t.Errorf("PostCreate = %q", be.PostCreate)
	}
	if len(be.Agent) != 1 || be.Agent[0] != "claude" {
		t.Errorf("Agent = %v, want [claude]", be.Agent)
	}
}

func TestCreateRefusesDuplicate(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Create("dup"); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	_, err := s.Create("dup")
	if !errors.Is(err, ErrExists) {
		t.Fatalf("second Create error = %v, want ErrExists", err)
	}
}

func TestLoadMissingTask(t *testing.T) {
	s := newTestStore(t)
	_, err := s.Load("nope")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Load error = %v, want ErrNotFound", err)
	}
}

func TestDeleteMissingTask(t *testing.T) {
	s := newTestStore(t)
	if err := s.Delete("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Delete error = %v, want ErrNotFound", err)
	}
}

// Save must not leave a partially written file behind, and must not leave temp
// files lying around in the tasks directory.
func TestSaveIsAtomicAndLeavesNoTempFiles(t *testing.T) {
	s := newTestStore(t)
	tk := New("atomic")
	for i := 0; i < 5; i++ {
		tk.SetRepo("r", Repo{Path: "/src/r", Branch: "agent/atomic", Worktree: "/wt/r"})
		if err := s.Save(tk); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	entries, err := os.ReadDir(s.TasksDir())
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("tasks dir has %d entries (%v), want exactly atomic.json", len(entries), names)
	}
	if entries[0].Name() != "atomic.json" {
		t.Errorf("leftover file %q", entries[0].Name())
	}
}

func TestListIsSortedAndSkipsNonTaskFiles(t *testing.T) {
	s := newTestStore(t)
	for _, n := range []string{"zeta", "alpha", "mid"} {
		if _, err := s.Create(n); err != nil {
			t.Fatalf("Create %s: %v", n, err)
		}
	}
	// Junk that must be ignored: a stray temp file and a non-JSON file.
	mustWrite(t, filepath.Join(s.TasksDir(), ".alpha.123.tmp"), "{}")
	mustWrite(t, filepath.Join(s.TasksDir(), "README.txt"), "not a task")

	tasks, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var got []string
	for _, tk := range tasks {
		got = append(got, tk.Name)
	}
	want := []string{"alpha", "mid", "zeta"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("List = %v, want %v", got, want)
	}
}

func TestListReportsUnreadableRecordsWithoutHidingTheRest(t *testing.T) {
	s := newTestStore(t)
	for _, n := range []string{"alpha", "zeta"} {
		if _, err := s.Create(n); err != nil {
			t.Fatalf("Create %s: %v", n, err)
		}
	}
	mustWrite(t, filepath.Join(s.TasksDir(), "corrupt.json"), "{not json")

	tasks, err := s.List()
	var skipped *SkippedError
	if !errors.As(err, &skipped) || len(skipped.Errs) != 1 {
		t.Fatalf("List = %v, want one skipped entry", err)
	}
	if len(tasks) != 2 || tasks[0].Name != "alpha" || tasks[1].Name != "zeta" {
		t.Errorf("readable tasks were dropped: %+v", tasks)
	}
}

// Records stay 0600 at every stage rather than widening to 0644 on the first
// update, matching the note files they point at.
func TestSavedRecordsAreOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not meaningful here")
	}
	s := newTestStore(t)
	created, err := s.Create("private")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	for _, stage := range []string{"created", "updated"} {
		info, err := os.Stat(filepath.Join(s.TasksDir(), "private.json"))
		if err != nil {
			t.Fatalf("stat (%s): %v", stage, err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Errorf("mode when %s = %o, want 600", stage, got)
		}
		created.Title = "second pass"
		if err := s.Save(created); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}
}

func TestListOnMissingDirectoryIsEmptyNotError(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "does-not-exist"))
	tasks, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("List = %v, want empty", tasks)
	}
}

func TestValidateNameRejectsUnsafeNames(t *testing.T) {
	bad := []string{
		"", ".", "..",
		"has/slash", `has\backslash`,
		"has:colon", "has space", "-leading-dash", "has?question",
		"has$dollar", "café",
	}
	for _, n := range bad {
		if err := ValidateName("task", n); err == nil {
			t.Errorf("ValidateName(%q) = nil, want error", n)
		}
	}

	// Dots are allowed: repo names legitimately contain them. The tmux layer
	// sanitizes them when building a session name, since tmux reads '.' as
	// target syntax.
	good := []string{"checkout-redesign", "payments_fix", "task123", "a", "checkout.redesign"}
	for _, n := range good {
		if err := ValidateName("task", n); err != nil {
			t.Errorf("ValidateName(%q) = %v, want nil", n, err)
		}
	}
}

func TestLoadRejectsUnsafeNameBeforeTouchingDisk(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Load("../../etc/passwd"); err == nil {
		t.Fatal("Load with traversal in name = nil, want error")
	}
}

func TestPathsFollowConvention(t *testing.T) {
	s := NewStore("/state")
	if got, want := s.WorktreePath("t1", "backend"), filepath.Join("/state", "worktrees", "t1", "backend"); got != want {
		t.Errorf("WorktreePath = %q, want %q", got, want)
	}
	// Reserved so worktrees can reach it as ../.context/ without a later migration.
	if got, want := s.ContextDir("t1"), filepath.Join("/state", "worktrees", "t1", ".context"); got != want {
		t.Errorf("ContextDir = %q, want %q", got, want)
	}
}

func TestDefaultRootHonoursAWRoot(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AW_ROOT", dir)
	got, err := DefaultRoot()
	if err != nil {
		t.Fatalf("DefaultRoot: %v", err)
	}
	if got != dir {
		t.Errorf("DefaultRoot = %q, want %q", got, dir)
	}
}

func TestRepoNamesAreSorted(t *testing.T) {
	tk := New("t")
	for _, n := range []string{"infra", "backend", "frontend"} {
		tk.SetRepo(n, Repo{})
	}
	got := strings.Join(tk.RepoNames(), ",")
	if got != "backend,frontend,infra" {
		t.Errorf("RepoNames = %s, want backend,frontend,infra", got)
	}
}

func TestAgentLabelDefaultsToShell(t *testing.T) {
	if got := (Repo{}).AgentLabel(); got != "shell" {
		t.Errorf("AgentLabel() = %q, want shell", got)
	}
	if got := (Repo{Agent: []string{"/usr/local/bin/claude", "--foo"}}).AgentLabel(); got != "claude" {
		t.Errorf("AgentLabel() = %q, want claude", got)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
