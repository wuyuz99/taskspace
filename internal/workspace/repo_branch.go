package workspace

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/wuyuz99/taskspace/internal/fspath"
	tsgit "github.com/wuyuz99/taskspace/internal/git"
	"github.com/wuyuz99/taskspace/internal/task"
)

// MoveRepoBranch renames repo's branch to branch and sets its base to base,
// either when not empty, in git as well as in the record, and says what it
// did. Every check runs before git changes anything. record saves the
// repository after each change git makes, so a failure part way leaves the
// record matching git, and the error says how far it got.
func (s *Service) MoveRepoBranch(ctx context.Context, t *task.Task, name string, r task.Repo, branch, base string, rebase bool, record func(task.Repo) error) (task.Repo, []string, error) {
	p, err := s.planBranchMove(ctx, t, name, r, branch, base, rebase)
	if err != nil {
		return r, nil, err
	}
	return s.moveBranch(ctx, t, name, r, p, record)
}

// branchMove is what MoveRepoBranch will do, decided before git changes
// anything.
type branchMove struct {
	branch           string
	renaming, rebase bool
	// present says the worktree exists.
	present bool
	// newBase is the base as a stable ref, if one was given.
	newBase string
	// oldBranchExists and upstream describe the branch being renamed.
	oldBranchExists bool
	upstream        string
	// onto and from are the rebase's commits.
	onto, from string
}

// planBranchMove runs every check of MoveRepoBranch.
func (s *Service) planBranchMove(ctx context.Context, t *task.Task, name string, r task.Repo, branch, base string, rebase bool) (*branchMove, error) {
	if rebase && r.BaseAssumed {
		return nil, fmt.Errorf("the base of %s was guessed; confirm its old base first with tspace repo update %s %s --base OLD_BASE (without --rebase)", name, t.Name, name)
	}
	p := &branchMove{branch: branch, renaming: branch != "" && branch != r.Branch, rebase: rebase}
	info, statErr := os.Stat(r.Worktree)
	p.present = statErr == nil && info.IsDir()
	var st tsgit.Status
	if p.renaming || rebase {
		var err error
		if st, err = s.Git.WorktreeStatus(ctx, r.Worktree); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		p.present = st.Exists
		// The worktree has the task's branch, or neither is touched.
		if p.present && st.Branch != r.Branch {
			return nil, fmt.Errorf("%s's worktree is not on %s (it has %s); switch back first", name, r.Branch, cmp.Or(st.Branch, "-"))
		}
	}
	// One fetch at most, shared by every lookup that wants one.
	fetched := false
	var fetchErr error
	fetch := func() error {
		if !fetched {
			fetched = true
			_, fetchErr = s.Git.FetchRemotes(ctx, r.Path, base)
		}
		return fetchErr
	}

	if base != "" {
		resolved, err := s.ResolveBase(ctx, r.Path, base, fetch)
		if err != nil {
			return nil, err
		}
		if p.newBase, err = s.stableRef(ctx, r.Path, resolved); err != nil {
			return nil, err
		}
	}
	if p.renaming {
		if err := s.checkBranchRename(ctx, name, r, p, fetch); err != nil {
			return nil, err
		}
	}
	if rebase {
		if err := s.checkRebase(ctx, name, r, st, p); err != nil {
			return nil, err
		}
	}
	return p, nil
}

// checkBranchRename refuses to rename r's branch to one that exists, here or
// on a remote, or one that is checked out elsewhere or has stashes, and notes
// the old branch's upstream.
func (s *Service) checkBranchRename(ctx context.Context, name string, r task.Repo, p *branchMove, fetch func() error) error {
	branch := p.branch
	if err := s.Git.CheckBranchName(ctx, branch); err != nil {
		return err
	}
	if exists, err := s.Git.BranchExists(ctx, r.Path, branch); err != nil {
		return err
	} else if exists {
		return fmt.Errorf("branch %s already exists in %s; repo update renames the task's branch and does not take over another", branch, r.Path)
	}
	// start would check a remote's branch of that name out and track it,
	// after a fetch, so one not fetched yet counts too.
	var fetchErr error
	remote, err := s.Git.RemoteBranch(ctx, r.Path, branch)
	if err == nil && remote == "" {
		if fetchErr = fetch(); fetchErr == nil {
			remote, err = s.Git.RemoteBranch(ctx, r.Path, branch)
		}
	}
	switch {
	case err != nil:
		return err
	case remote != "":
		return fmt.Errorf("%s already exists as %s; repo update renames the task's branch and does not take over another", branch, remote)
	case fetchErr != nil:
		s.warnf("note: could not fetch to check that no remote has a branch %s (%v); if one does, start will track it", branch, fetchErr)
	}
	if p.oldBranchExists, err = s.Git.BranchExists(ctx, r.Path, r.Branch); err != nil || !p.oldBranchExists {
		return err
	}
	// Checked out anywhere but the task's own worktree, it is someone else's
	// to rename, the user's clone included.
	if wt, err := s.Git.FindWorktreeForBranch(ctx, r.Path, r.Branch); err != nil {
		return err
	} else if wt != nil && !fspath.Same(wt.Path, r.Worktree) {
		return fmt.Errorf("%s is checked out at %s, outside the task; repo update will not rename it there", r.Branch, wt.Path)
	}
	// Stashes are found by the branch named in their message, in a list every
	// worktree shares.
	if n, err := s.Git.StashCount(ctx, r.Path, r.Branch); err != nil {
		return err
	} else if n > 0 {
		return fmt.Errorf("%s has %d stash%s made on %s, which would no longer be counted as its work once renamed; apply or drop them first", name, n, StashPlural(n), r.Branch)
	}
	p.upstream, err = s.Git.Upstream(ctx, r.Path, r.Branch)
	return err
}

// checkRebase refuses a rebase with nothing to rebase, uncommitted changes,
// an operation under way or a base that no longer says where the branch
// started, and works out its commits.
func (s *Service) checkRebase(ctx context.Context, name string, r task.Repo, st tsgit.Status, p *branchMove) error {
	switch {
	case !p.present:
		return fmt.Errorf("%s has no worktree yet, so there is nothing to rebase: start creates the branch from the base; drop --rebase", name)
	case st.DirtyCount() > 0:
		return fmt.Errorf("%s has %d uncommitted change%s in %s; commit or stash them before --rebase", name, st.DirtyCount(), Plural(st.DirtyCount()), r.Worktree)
	}
	if op, err := s.Git.InProgress(ctx, r.Worktree); err != nil {
		return err
	} else if op != "" {
		return fmt.Errorf("%s is in the middle of a %s; finish it before --rebase", name, op)
	}
	// The old base must still mean what it meant when it was recorded: HEAD,
	// say, now names whatever the source clone has checked out.
	old, err := s.stableRef(ctx, r.Path, r.Base)
	if err != nil || old != r.Base {
		return fmt.Errorf("the recorded base of %s, %q, does not say where its branch started; rebase by hand, then run this without --rebase", name, r.Base)
	}
	// Commits, resolved in the source repository. The branch's work is what
	// it has beyond where it forked from its old base, even if that base has
	// moved or been rewritten since.
	if p.onto, err = s.Git.Commit(ctx, r.Path, p.newBase); err != nil {
		return err
	}
	oldCommit, err := s.Git.Commit(ctx, r.Path, r.Base)
	if err != nil {
		return fmt.Errorf("the old base of %s no longer resolves (%v); rebase by hand, then run this without --rebase", name, err)
	}
	p.from, err = s.Git.MergeBase(ctx, r.Path, "refs/heads/"+r.Branch, oldCommit)
	return err
}

// moveBranch carries out p, recording each change git makes as it is made.
func (s *Service) moveBranch(ctx context.Context, t *task.Task, name string, r task.Repo, p *branchMove, record func(task.Repo) error) (task.Repo, []string, error) {
	branch, newBase := p.branch, p.newBase
	var notes []string
	if p.rebase {
		undone, err := s.Git.Rebase(ctx, r.Worktree, p.onto, p.from)
		if err != nil {
			if !undone {
				return r, nil, fmt.Errorf("%s: %w", name, err)
			}
			return r, nil, fmt.Errorf("%s: %w\nnothing was changed; to rebase by hand, in %s run: git rebase --onto %s %s\nthen record the base with: tspace repo update %s %s --base %s",
				name, err, r.Worktree, newBase, p.from, t.Name, name, newBase)
		}
		r.Base = newBase
		if err := record(r); err != nil {
			return r, nil, fmt.Errorf("rebased %s onto %s, but could not record its new base: %w; record it with: tspace repo update %s %s --base %s", r.Branch, newBase, err, t.Name, name, newBase)
		}
		notes = append(notes, fmt.Sprintf("base: %s (rebased %s onto it)", newBase, r.Branch))
	}
	if p.renaming {
		old := r.Branch
		done := ""
		if p.rebase {
			done = fmt.Sprintf("rebased %s onto %s and recorded that, but ", old, newBase)
		}
		if p.oldBranchExists {
			if err := s.Git.RenameBranch(ctx, r.Path, old, branch); err != nil {
				return r, notes, fmt.Errorf("%scould not rename it: %w", done, err)
			}
			notes = append(notes, fmt.Sprintf("branch: renamed %s to %s", old, branch))
			// Tracking the old name's remote branch, it would compare with
			// and push to that.
			if p.upstream != "" {
				if err := s.Git.UnsetUpstream(ctx, r.Path, branch); err != nil {
					s.warnf("note: %s still tracks %s: %v", branch, p.upstream, err)
				} else {
					notes = append(notes, fmt.Sprintf("branch: no longer tracks %s; push it under its new name with: git push -u %s %s", p.upstream, strings.SplitN(p.upstream, "/", 2)[0], branch))
				}
			}
		} else {
			notes = append(notes, fmt.Sprintf("branch: %s (start creates it)", branch))
		}
		r.Branch = branch
		if err := record(r); err != nil {
			if p.oldBranchExists {
				return r, notes, fmt.Errorf("%srenamed %s to %s in git, and could not record it: %w; to undo the rename, run: git -C %s branch -m %s %s", done, old, branch, err, r.Path, branch, old)
			}
			return r, notes, err
		}
	}
	if newBase != "" && !p.rebase {
		r.Base = newBase
		r.BaseAssumed = false
		if p.present {
			notes = append(notes, fmt.Sprintf("base: %s (%s is unchanged; status and clean now compare it with %s; --rebase moves its commits onto it)", newBase, r.Branch, newBase))
		} else {
			notes = append(notes, fmt.Sprintf("base: %s (start creates the branch from it if it does not exist yet)", newBase))
		}
	}
	return r, notes, nil
}

// stableRef is ref as a name that means the same commit in every worktree
// and only moves when that branch or tag does: a branch, a remote's branch,
// a tag, or a commit ID. It refuses HEAD~1, FETCH_HEAD and the like, and
// spells HEAD and @{-1} as the branch they name now.
func (s *Service) stableRef(ctx context.Context, repo, ref string) (string, error) {
	if isObjectID(ref) {
		if _, err := s.Git.Commit(ctx, repo, ref); err != nil {
			return "", err
		}
		return ref, nil
	}
	full, err := s.Git.FullRefName(ctx, repo, ref)
	if err != nil {
		return "", err
	}
	for _, prefix := range []string{"refs/heads/", "refs/remotes/", "refs/tags/"} {
		if short, ok := strings.CutPrefix(full, prefix); ok {
			return short, nil
		}
	}
	return "", fmt.Errorf("%q moves on its own, or means something else in each worktree; give a branch, tag or commit as the base", ref)
}

func isObjectID(s string) bool {
	if len(s) < 7 || len(s) > 64 {
		return false
	}
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}

// ResolveBase turns a base the user typed into a ref that resolves in repo,
// fetching once if it is not there yet: by fetch, when the caller shares one
// fetch between several lookups, else by fetching the remotes itself.
func (s *Service) ResolveBase(ctx context.Context, repo, ref string, fetch func() error) (string, error) {
	resolved, err := s.Git.ResolveRef(ctx, repo, ref)
	if err != nil || resolved != "" {
		return resolved, err
	}
	if fetch == nil {
		fetch = func() error {
			_, err := s.Git.FetchRemotes(ctx, repo, ref)
			return err
		}
	}
	fetchErr := fetch()
	if resolved, err = s.Git.ResolveRef(ctx, repo, ref); err == nil && resolved == "" {
		err = fmt.Errorf("base %q is not a ref in %s, locally or on a remote%s", ref, repo, FetchNote(fetchErr))
	}
	return resolved, err
}

// FetchNote says a lookup used stale refs because fetching failed.
func FetchNote(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf(" (fetch failed: %v)", err)
}
