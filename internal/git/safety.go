package git

import (
	"context"
	"fmt"

	awexec "github.com/aw-tool/aw/internal/exec"
)

// Discardability answers "can this branch be deleted without losing work?"
type Discardability struct {
	Branch string
	Base   string

	Merged   bool   // an ancestor of the base branch
	Pushed   bool   // has an upstream with nothing unpushed
	Upstream string // "" when the branch has no upstream

	// UnpushedCommits is meaningful only when the branch has an upstream.
	UnpushedCommits int
}

// Safe reports whether the branch's work exists somewhere else.
func (d Discardability) Safe() bool { return d.Merged || d.Pushed }

// Reason explains the verdict in one line, for CLI output.
func (d Discardability) Reason() string {
	switch {
	case d.Merged:
		return fmt.Sprintf("merged into %s", d.Base)
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
			merged, err := g.ok(ctx, repo, "merge-base", "--is-ancestor", branch, base)
			if err != nil {
				return d, err
			}
			d.Merged = merged
		}
	}

	upstream, err := g.upstreamOf(ctx, repo, branch)
	if err != nil {
		return d, err
	}
	d.Upstream = upstream

	if upstream != "" {
		res, err := g.git(ctx, repo, "rev-list", "--count", upstream+".."+branch)
		if err != nil {
			return d, fmt.Errorf("count unpushed commits on %s: %w", branch, err)
		}
		n := 0
		if _, scanErr := fmt.Sscanf(res.Out(), "%d", &n); scanErr != nil {
			return d, fmt.Errorf("parse commit count for %s: %q", branch, res.Out())
		}
		d.UnpushedCommits = n
		d.Pushed = n == 0
	}
	return d, nil
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
