package workspace

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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

// RepoSpec is one repository as typed on the command line:
//
//	[NAME=]SOURCE[@BRANCH][:BASE]
//
// SOURCE is a registered name or a path. The spec splits at the first '@',
// which no repository name contains, and then at the first ':', which git
// forbids in ref names; a branch keeps any later '@' of its own.
type RepoSpec struct {
	Name, Source, Branch, Base string
}

const repoSpecForm = "[NAME=]REPO[@BRANCH][:BASE]"

// ParseRepoSpec reads one repository as typed.
func ParseRepoSpec(spec string) (RepoSpec, error) {
	var s RepoSpec
	rest := spec
	// '=' separates a name only when what precedes it could be one, since a
	// branch name may itself contain '='.
	if name, source, found := strings.Cut(spec, "="); found && !strings.ContainsAny(name, "@:/") {
		if name == "" || source == "" {
			return s, fmt.Errorf("invalid repository %q: expected %s", spec, repoSpecForm)
		}
		s.Name, rest = name, source
	}
	// A directory that exists as typed is taken literally, so a path that
	// happens to contain '@' or ':' still works.
	if looksLikePath(rest) && isDir(ExpandHome(rest)) {
		s.Source = rest
		return s, nil
	}

	source, branch, hasBranch := strings.Cut(rest, "@")
	tail := source
	if hasBranch {
		tail = branch
	}
	head, base, hasBase := strings.Cut(tail, ":")
	if hasBranch {
		s.Source, s.Branch = source, head
	} else {
		s.Source = head
	}
	s.Base = base
	if s.Source == "" || (hasBranch && s.Branch == "") || (hasBase && s.Base == "") {
		return s, fmt.Errorf("invalid repository %q: expected %s", spec, repoSpecForm)
	}
	return s, nil
}

func looksLikePath(s string) bool {
	return strings.ContainsRune(s, '/') || strings.HasPrefix(s, "~") || strings.HasPrefix(s, ".") || filepath.IsAbs(s)
}

// AddRepoSpec parses a REPO argument and adds it. A part given both in the
// spec and as a flag is refused rather than one silently winning.
func (s *Service) AddRepoSpec(ctx context.Context, t *task.Task, c *task.Config, spec string, opts AddRepoOptions) error {
	r, err := ParseRepoSpec(spec)
	if err != nil {
		return err
	}
	for _, p := range []struct{ flag, fromSpec, fromFlag string }{
		{"--name", r.Name, opts.Name}, {"--branch", r.Branch, opts.Branch}, {"--base", r.Base, opts.Base},
	} {
		if p.fromSpec != "" && p.fromFlag != "" {
			return fmt.Errorf("%s is given twice, in %q and as %s", strings.TrimPrefix(p.flag, "--"), spec, p.flag)
		}
	}
	opts.Name = cmp.Or(opts.Name, r.Name)
	opts.Branch = cmp.Or(opts.Branch, r.Branch)
	opts.Base = cmp.Or(opts.Base, r.Base)

	path := r.Source
	if !looksLikePath(r.Source) {
		if p, ok := c.Repos[r.Source]; ok {
			path = p
			opts.Name = cmp.Or(opts.Name, r.Source)
		} else if !isDir(r.Source) {
			return fmt.Errorf("no repository registered as %q%s", r.Source, RegisteredHint(c))
		}
	}
	return s.AddRepo(ctx, t, c, ExpandHome(path), opts)
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// ExpandHome resolves a leading ~ so that paths typed in a shell that did not
// expand them still work.
func ExpandHome(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") && !strings.HasPrefix(path, `~\`) {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if path == "~" {
		return home
	}
	return filepath.Join(home, path[2:])
}
