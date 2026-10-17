package task

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectPersistenceValidation(t *testing.T) {
	s := newTestStore(t)
	if ps, err := s.ListProjects(); err != nil || len(ps) != 0 {
		t.Fatalf("empty list: %v", err)
	}
	for _, name := range []string{"zeta", "alpha"} {
		p := &Project{Name: name, Repos: map[string]ProjectRepo{"repo": {Path: "/src/repo"}}}
		if err := s.SaveProject(p, false); err != nil {
			t.Fatal(err)
		}
		if err := s.SaveProject(p, false); !errors.Is(err, os.ErrExist) {
			t.Fatalf("duplicate: %v", err)
		}
	}
	ps, err := s.ListProjects()
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 2 || ps[0].Name != "alpha" {
		t.Fatalf("list: %+v", ps)
	}
	if _, err := s.LoadProject("../escape"); err == nil {
		t.Fatal("unsafe name accepted")
	}
	if err := s.SaveProject(&Project{Name: "empty"}, false); err == nil {
		t.Fatal("empty project accepted")
	}
	entries, err := os.ReadDir(s.ProjectsDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatal("leftover temporary files")
	}
	if err := os.WriteFile(filepath.Join(s.ProjectsDir(), "bad.json"), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LoadProject("bad"); err == nil {
		t.Fatal("malformed JSON accepted")
	}
	// One unreadable recipe must not hide the rest of the shelf.
	ps, err = s.ListProjects()
	var skipped *SkippedError
	if !errors.As(err, &skipped) || len(skipped.Errs) != 1 {
		t.Fatalf("list with a malformed recipe = %v, want one skipped entry", err)
	}
	if len(ps) != 2 || ps[0].Name != "alpha" {
		t.Fatalf("readable recipes were dropped: %+v", ps)
	}
}

func TestCreatePreparedRefusesOverwrite(t *testing.T) {
	s := newTestStore(t)
	original := New("t")
	original.Title = "keep me"
	if err := s.CreatePrepared(original); err != nil {
		t.Fatal(err)
	}
	if err := s.CreatePrepared(New("t")); !errors.Is(err, os.ErrExist) {
		t.Fatalf("overwrite: %v", err)
	}
	got, err := s.Load("t")
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "keep me" {
		t.Fatal("task overwritten")
	}
}

// A recipe saved from a task keeps its configuration and none of its identity.
func TestProjectFromTask(t *testing.T) {
	tk := New("checkout")
	tk.Title = "not carried over"
	tk.Agent = []string{"claude"}
	tk.SetRepo("frontend", Repo{Path: "/src/fe", Branch: "agent/checkout", Worktree: "/w/fe"})
	tk.SetRepo("backend", Repo{Path: "/src/be", Base: "origin/main", Agent: []string{"codex"}, PostCreate: "make deps"})

	p := ProjectFromTask("shop", tk)
	if p.Name != "shop" || len(p.Agent) != 1 || p.Agent[0] != "claude" {
		t.Fatalf("project = %+v", p)
	}
	if got := strings.Join(p.RepoNames(), ","); got != "backend,frontend" {
		t.Errorf("RepoNames = %s", got)
	}
	be := p.Repos["backend"]
	if be.Path != "/src/be" || be.Base != "origin/main" || be.PostCreate != "make deps" || be.Agent[0] != "codex" {
		t.Errorf("backend = %+v", be)
	}

	// The recipe is a copy: changing it must not reach back into the task.
	p.Agent[0] = "changed"
	be.Agent[0] = "changed"
	if tk.Agent[0] != "claude" || tk.Repos["backend"].Agent[0] != "codex" {
		t.Fatal("project shares slices with the task")
	}
}

func TestProjectValidateRejectsBadRepos(t *testing.T) {
	s := newTestStore(t)
	for name, p := range map[string]*Project{
		"bad repo name": {Name: "p", Repos: map[string]ProjectRepo{"../x": {Path: "/src"}}},
		"blank path":    {Name: "p", Repos: map[string]ProjectRepo{"repo": {Path: "  "}}},
		"bad name":      {Name: "a b", Repos: map[string]ProjectRepo{"repo": {Path: "/src"}}},
	} {
		if err := s.SaveProject(p, false); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := s.LoadProject("missing"); err == nil {
		t.Error("missing project loaded")
	}
	// A recipe edited by hand into an invalid one is refused on load.
	if err := os.MkdirAll(s.ProjectsDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(s.ProjectsDir(), "hollow.json"), `{"repos":{}}`)
	if _, err := s.LoadProject("hollow"); err == nil {
		t.Error("project with no repositories loaded")
	}
}

func TestProjectsDirectoryErrors(t *testing.T) {
	s := newTestStore(t)
	mustWrite(t, s.ProjectsDir(), "")
	p := &Project{Name: "p", Repos: map[string]ProjectRepo{"repo": {Path: "/src"}}}
	if err := s.SaveProject(p, true); err == nil {
		t.Error("SaveProject wrote under a file")
	}
	if err := s.CreatePrepared(New("../escape")); err == nil {
		t.Error("CreatePrepared accepted an unsafe name")
	}
}

func TestSaveProjectReplace(t *testing.T) {
	s := newTestStore(t)
	p := &Project{Name: "p", Repos: map[string]ProjectRepo{"repo": {Path: "/one"}}}
	if err := s.SaveProject(p, false); err != nil {
		t.Fatal(err)
	}
	p.Repos["repo"] = ProjectRepo{Path: "/two"}
	if err := s.SaveProject(p, true); err != nil {
		t.Fatalf("replace: %v", err)
	}
	got, err := s.LoadProject("p")
	if err != nil {
		t.Fatal(err)
	}
	if got.Repos["repo"].Path != "/two" {
		t.Errorf("Path = %q, want /two", got.Repos["repo"].Path)
	}
}
