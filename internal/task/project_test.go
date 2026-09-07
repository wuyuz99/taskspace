package task

import (
	"errors"
	"os"
	"path/filepath"
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
