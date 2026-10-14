package git

import (
	"context"
	"fmt"
	"strings"

	awexec "github.com/wuyuz99/taskspace/internal/exec"
)

// Discardability answers "can this branch be deleted without losing work?"
type Discardability struct {
	Branch string
	Base   string

	Merged   bool   // an ancestor of the base branch
	Squashed bool   // its combined change is a single commit on the base branch
	Pushed   bool   // has an upstream with nothing unpushed
	Upstream string // "" when the branch has no upstream

	// UnpushedCommits is meaningful only when the branch has an upstream.
	UnpushedCommits int
}

// Safe reports whether the branch's work exists somewhere else.
func (d Discardability) Safe() bool { return d.Merged || d.Squashed || d.Pushed }

// HeadReferenced reports whether HEAD is reachable from a branch, tag, or
// remote-tracking ref that will survive cleanup. Reflogs and other worktree
// HEADs are not durable protection for a detached commit.
func (g *Git) HeadReferenced(ctx context.Context, worktree, excludedBranch string) (bool, error) {
	return g.refSurvivesDeletion(ctx, worktree, "HEAD", excludedBranch)
}

// A commit ID or symbolic alias alone does not keep work reachable. Require a
// containing ref other than the branch being removed or an alias of it.
func (g *Git) refSurvivesDeletion(ctx context.Context, repo, ref, excludedBranch string) (bool, error) {
	res, err := g.git(ctx, repo, "for-each-ref", "--contains="+ref, "--format=%(refname) %(symref)", "refs/heads/", "refs/tags/", "refs/remotes/")
	if err != nil {
		return false, fmt.Errorf("verify surviving reference for %s in %s: %w", ref, repo, err)
	}
	for _, line := range strings.Split(res.Stdout, "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 && fields[0] != "refs/heads/"+excludedBranch &&
			(len(fields) == 1 || fields[1] != "refs/heads/"+excludedBranch) {
			return true, nil
		}
	}
	return false, nil
}

// Reason explains the verdict in one line, for CLI output.
func (d Discardability) Reason() string {
	switch {
	case d.Merged:
		return fmt.Sprintf("merged into %s", d.Base)
	case d.Squashed:
		return fmt.Sprintf("squash-merged into %s", d.Base)
	case d.Pushed:
		return fmt.Sprintf("pushed to %s", d.Upstream)
	case d.Upstream != "":
		return fmt.Sprintf("%d commit(s) not pushed to %s and not merged into %s",
			d.UnpushedCommits, d.Upstream, d.Base)
	default:
		return fmt.Sprintf("not merged into %s and never pushed", d.Base)
	}
}

// CanDiscardBranch decides whether deleting branch would lose work.
//
// Work is safe to discard when it is merged into base, or pushed to a tracked
// upstream. Both halves are needed: without the upstream test, a branch that
// was pushed but never merged looks unique forever; without the merge test, a
// merged local-only branch could never be cleaned. Anything failing both is
// unique work and the caller must refuse unless the user forces it.
func (g *Git) CanDiscardBranch(ctx context.Context, repo, branch, base string) (Discardability, error) {
	d := Discardability{Branch: branch, Base: base}

	exists, err := g.BranchExists(ctx, repo, branch)
	if err != nil {
		return d, err
	}
	if !exists {
		// Nothing to lose.
		d.Merged = true
		return d, nil
	}

	if base != "" {
		baseExists, err := g.RefExists(ctx, repo, base)
		if err != nil {
			return d, err
		}
		if baseExists {
			baseExists, err = g.refSurvivesDeletion(ctx, repo, base, branch)
			if err != nil {
				return d, err
			}
		}
		if baseExists {
			merged, err := g.ok(ctx, repo, "merge-base", "--is-ancestor", "refs/heads/"+branch, base)
			if err != nil {
				return d, err
			}
			d.Merged = merged
			if !merged {
				if d.Squashed, err = g.squashMerged(ctx, repo, branch, base); err != nil {
					return d, err
				}
			}
		}
	}

	upstream, err := g.upstreamOf(ctx, repo, branch)
	if err != nil {
		return d, err
	}
	d.Upstream = upstream

	if upstream != "" {
		res, err := g.git(ctx, repo, "rev-list", "--count", upstream+"..refs/heads/"+branch)
		if err != nil {
			return d, fmt.Errorf("count unpushed commits on %s: %w", branch, err)
		}
		n := 0
		if _, scanErr := fmt.Sscanf(res.Out(), "%d", &n); scanErr != nil {
			return d, fmt.Errorf("parse commit count for %s: %q", branch, res.Out())
		}
		d.UnpushedCommits = n
		if n == 0 {
			d.Pushed, err = g.refSurvivesDeletion(ctx, repo, upstream, branch)
			if err != nil {
				return d, err
			}
		}
	}
	return d, nil
}

// squashMerged reports whether base holds a commit making exactly the change
// the branch makes, which is what a squash merge leaves behind. The branch's
// own commits are then never ancestors of base, and a hosting service usually
// deletes the remote branch too, so neither other test can see the work landed.
//
// The branch's cumulative change is written as one commit on its merge base,
// and `git cherry` looks for a commit with the same patch-id on base. The probe
// commit is unreferenced and left for git's garbage collection. A squash that
// needed conflict resolution produces a different patch and is not recognised,
// which errs toward keeping the branch.
func (g *Git) squashMerged(ctx context.Context, repo, branch, base string) (bool, error) {
	res, err := g.git(ctx, repo, "merge-base", base, branch)
	if err != nil {
		if awexec.IsExit(err) {
			return false, nil // unrelated histories
		}
		return false, err
	}
	mergeBase := res.Out()
	res, err = g.git(ctx, repo, "rev-parse", branch+"^{tree}")
	if err != nil {
		return false, err
	}
	// commit-tree needs an identity, and the user may not have configured one.
	// It also honours commit.gpgSign, which would prompt for a passphrase to
	// sign a probe nobody will ever see.
	res, err = g.git(ctx, repo, "-c", "user.name=aw", "-c", "user.email=aw@localhost",
		"commit-tree", "--no-gpg-sign", res.Out(), "-p", mergeBase, "-m", "aw squash-merge probe")
	if err != nil {
		return false, fmt.Errorf("check whether %s was squash-merged: %w", branch, err)
	}
	res, err = g.git(ctx, repo, "cherry", base, res.Out())
	if err != nil {
		return false, fmt.Errorf("check whether %s was squash-merged: %w", branch, err)
	}
	return strings.HasPrefix(res.Out(), "- "), nil
}

// upstreamOf returns the configured upstream of a branch, or "".
func (g *Git) upstreamOf(ctx context.Context, repo, branch string) (string, error) {
	res, err := g.git(ctx, repo, "rev-parse", "--abbrev-ref", "--symbolic-full-name", branch+"@{upstream}")
	if err != nil {
		if awexec.IsExit(err) {
			// No upstream configured is an expected, non-exceptional state.
			return "", nil
		}
		return "", err
	}
	return res.Out(), nil
}
