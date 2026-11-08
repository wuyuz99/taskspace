package workspace

import (
	"context"
	"fmt"

	"github.com/wuyuz99/taskspace/internal/fspath"
	"github.com/wuyuz99/taskspace/internal/task"
)

// updateReference is UpdateRepo for a reference: Base moves it to a new
// base, or the latest of its own after a fetch, and Branch makes it a working
// repository on a new branch from where it is. It has no agent, resume
// command or hook to change, and no commits of its own to rebase.
func (s *Service) updateReference(ctx context.Context, t *task.Task, name string, r task.Repo, opts UpdateRepoOptions) (*UpdateRepoResult, error) {
	switch {
	case opts.SetAgent || opts.Shell || opts.SetResume || opts.NoResume || opts.SetHook:
		return nil, fmt.Errorf("%s is a reference, which has no window, agent or hook; make it a working repository first with: tspace repo update %s %s --branch BRANCH", name, t.Name, name)
	case opts.Rebase:
		return nil, fmt.Errorf("%s is a reference, with no commits of its own to rebase; --base alone moves it", name)
	}
	st, err := s.Git.WorktreeStatus(ctx, r.Worktree)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	var base string
	if opts.Base != "" {
		if base, err = s.referenceBase(ctx, name, r, opts.Base, st.Exists); err != nil {
			return nil, err
		}
	}
	if opts.Branch != "" {
		if err := s.checkNewBranch(ctx, r, opts.Branch); err != nil {
			return nil, err
		}
	}

	var notes []string
	// Writable while git changes it; a reference that stays one is made
	// read-only again after.
	if st.Exists {
		if err := fspath.SetReadOnly(r.Worktree, false); err != nil {
			return nil, fmt.Errorf("%s: make its files writable: %w", name, err)
		}
	}
	reprotect := st.Exists && opts.Branch == ""
	if base != "" {
		if st.Exists {
			if err := s.Git.CheckoutDetached(ctx, r.Worktree, base); err != nil {
				if reprotect {
					s.protect(name, r.Worktree)
				}
				return nil, fmt.Errorf("%s: %w", name, err)
			}
			notes = append(notes, fmt.Sprintf("base: %s (checked out)", base))
		} else {
			notes = append(notes, fmt.Sprintf("base: %s (start checks it out)", base))
		}
		r.Base = base
		t.SetRepo(name, r)
		if err := s.Store.Save(t); err != nil {
			return nil, err
		}
	}
	if reprotect {
		s.protect(name, r.Worktree)
	}
	if opts.Branch != "" {
		if st.Exists {
			if err := s.Git.CreateBranchHere(ctx, r.Worktree, opts.Branch); err != nil {
				s.protect(name, r.Worktree)
				return nil, fmt.Errorf("%s: %w", name, err)
			}
			notes = append(notes, fmt.Sprintf("branch: created %s where it was checked out; its files are writable", opts.Branch))
		} else {
			notes = append(notes, fmt.Sprintf("branch: %s (start creates it from %s)", opts.Branch, r.Base))
		}
		r.Reference, r.Branch = false, opts.Branch
		t.SetRepo(name, r)
		if err := s.Store.Save(t); err != nil {
			if st.Exists {
				return nil, fmt.Errorf("created %s in %s, and could not record it: %w", opts.Branch, r.Worktree, err)
			}
			return nil, err
		}
		notes = append(notes, fmt.Sprintf("it is a working repository now; open its window with: tspace start %s %s", t.Name, name))
	}
	return &UpdateRepoResult{Task: t, Notes: notes}, nil
}

// referenceBase is the base a reference moves to, after fetching so that the
// same base given again brings it up to date. Its worktree, if there is one,
// must hold nothing that moving would lose.
func (s *Service) referenceBase(ctx context.Context, name string, r task.Repo, base string, present bool) (string, error) {
	_, fetchErr := s.Git.FetchRemotes(ctx, r.Path, base)
	if fetchErr != nil {
		s.warnf("note: could not fetch, so %s is where it was when last fetched: %v", base, fetchErr)
	}
	resolved, err := s.ResolveBase(ctx, r.Path, base, func() error { return fetchErr })
	if err != nil {
		return "", err
	}
	if resolved, err = s.stableRef(ctx, r.Path, resolved); err != nil {
		return "", err
	}
	if !present {
		return resolved, nil
	}
	st, err := s.Git.WorktreeStatus(ctx, r.Worktree)
	if err != nil {
		return "", fmt.Errorf("%s: %w", name, err)
	}
	if n := st.DirtyCount(); n > 0 {
		return "", fmt.Errorf("%s has %d change%s in %s although it is a reference; look at them, and remove them or make it a working repository with --branch", name, n, Plural(n), r.Worktree)
	}
	if safe, err := s.Git.HeadReferenced(ctx, r.Worktree, ""); err != nil {
		return "", fmt.Errorf("%s: %w", name, err)
	} else if !safe {
		return "", fmt.Errorf("%s has a commit made in it that no branch or tag holds; keep it with --branch, or create a branch at HEAD first", name)
	}
	return resolved, nil
}

// checkNewBranch refuses a branch name that is invalid or already taken,
// here or on a remote: a reference becomes a working repository on a branch
// of its own, never one that holds other work.
func (s *Service) checkNewBranch(ctx context.Context, r task.Repo, branch string) error {
	if err := s.Git.CheckBranchName(ctx, branch); err != nil {
		return err
	}
	if exists, err := s.Git.BranchExists(ctx, r.Path, branch); err != nil {
		return err
	} else if exists {
		return fmt.Errorf("branch %s already exists in %s; give a new one", branch, r.Path)
	}
	if remote, err := s.Git.RemoteBranch(ctx, r.Path, branch); err != nil {
		return err
	} else if remote != "" {
		return fmt.Errorf("%s already exists as %s; give a new branch", branch, remote)
	}
	return nil
}
