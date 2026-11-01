package workspace

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/wuyuz99/taskspace/internal/task"
)

// AddRepoOptions choose how AddRepo attaches a repository.
type AddRepoOptions struct {
	Name       string
	Branch     string
	Base       string
	Agent      []string
	Resume     []string
	PostCreate string
}

// RepoAdded reports a repository attached to a task. BranchNote says what
// start will do with its branch, and Existing that the branch is already
// there, so Base is what it is compared with, not created from.
type RepoAdded struct {
	Task, Repo, Path, Branch, BranchNote, Base, Worktree string
	Existing, BaseAssumed                                bool
}

// WorktreeReused reports that a repository's branch is already checked out at
// its own worktree, which start will reuse.
type WorktreeReused struct{ Branch, Path string }

func (RepoAdded) event()      {}
func (WorktreeReused) event() {}

// AddRepo validates the repository at path and records it on t, in memory:
// the caller saves t, as it may be changing more of it. It reports what start
// will do with the branch, and creates nothing on disk.
func (s *Service) AddRepo(ctx context.Context, t *task.Task, c *task.Config, path string, opts AddRepoOptions) error {
	canonical, err := s.Git.Validate(ctx, path)
	if err != nil {
		return err
	}

	name := opts.Name
	if name == "" {
		name = filepath.Base(canonical)
	}
	if err := task.ValidateName("repo", name); err != nil {
		return err
	}
	if existing, ok := t.Repo(name); ok {
		return fmt.Errorf("task %q already has a repo named %q (%s); use --name to pick another",
			t.Name, name, existing.Path)
	}
	for _, other := range t.RepoNames() {
		if t.Repos[other].Path == canonical {
			return fmt.Errorf("%s is already attached to task %q as %q", canonical, t.Name, other)
		}
	}

	branch := opts.Branch
	if branch == "" {
		branch = c.Branch(t.Name)
	}
	if err := s.Git.CheckBranchName(ctx, branch); err != nil {
		return err
	}

	// Refs the user named but this clone has not fetched yet are the ones
	// most likely to be new, so fetch once before calling them missing.
	fetched := false
	var fetchErr error
	fetch := func() error {
		if !fetched {
			fetched = true
			_, fetchErr = s.Git.FetchRemotes(ctx, canonical, opts.Base)
		}
		return fetchErr
	}

	// Resolve the base now so that `tspace task show` can report it before anything
	// is created, and so a later start cannot silently pick a different one.
	base := opts.Base
	if base == "" {
		base, err = s.Git.ResolveBase(ctx, canonical)
	} else {
		base, err = s.ResolveBase(ctx, canonical, opts.Base, fetch)
	}
	if err != nil {
		return err
	}

	// Report what start will do with the branch, so a mistyped existing branch
	// shows up now as "new", not later as a worktree without the work in it.
	local, err := s.Git.BranchExists(ctx, canonical, branch)
	if err != nil {
		return err
	}
	remote := ""
	if !local {
		if remote, err = s.Git.RemoteBranch(ctx, canonical, branch); err == nil && remote == "" && opts.Branch != "" {
			_ = fetch()
			remote, err = s.Git.RemoteBranch(ctx, canonical, branch)
		}
		if err != nil {
			return err
		}
	}
	assumed := opts.Base == "" && (local || remote != "")
	if assumed {
		target := remote
		if local {
			target = "refs/heads/" + branch
		}
		base, err = s.Git.InferBase(ctx, canonical, branch, target, base)
		if err != nil {
			return err
		}
	}
	var branchNote string
	switch {
	case local:
		branchNote = "existing branch"
	case remote != "":
		branchNote = "will track " + remote
	default:
		branchNote = "new, from " + base
		if opts.Branch != "" {
			s.warnf("note: %s has no branch %s locally or on a remote%s; start will create it from %s",
				name, branch, FetchNote(fetchErr), base)
		}
	}
	worktree := s.Store.WorktreePath(t.Name, name)
	if local {
		// Checked out at this repo's own worktree path, as after repo rm and
		// add again, start reuses the worktree; only elsewhere does it refuse.
		if wt, err := s.Git.FindWorktreeForBranch(ctx, canonical, branch); err == nil && wt != nil {
			if own, err := s.Git.FindWorktree(ctx, canonical, worktree); err == nil && own != nil && own.Branch == branch {
				s.report(WorktreeReused{Branch: branch, Path: own.Path})
			} else {
				s.warnf("note: %s is checked out at %s; start will refuse until it is switched away there", branch, wt.Path)
			}
		}
	}

	t.SetRepo(name, task.Repo{
		Path:        canonical,
		Branch:      branch,
		Base:        base,
		BaseAssumed: assumed,
		Worktree:    worktree,
		Agent:       opts.Agent,
		Resume:      opts.Resume,
		PostCreate:  opts.PostCreate,
	})

	s.report(RepoAdded{
		Task: t.Name, Repo: name, Path: canonical, Branch: branch, BranchNote: branchNote,
		Base: base, Existing: local || remote != "", BaseAssumed: assumed, Worktree: worktree,
	})
	return nil
}
