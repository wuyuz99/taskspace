package git

import (
	"context"
	"strings"

	tsexec "github.com/wuyuz99/taskspace/internal/exec"
)

// InferBase guesses the closest parent among local and remote branches.
// Git does not store ancestry intent: callers must mark this as assumed.
// The default wins ties, followed by Git's deterministic ref-name order.
// The guess is named as ResolveBase names a base, without the refs/ prefix.
func (g *Git) InferBase(ctx context.Context, repo, branch, target, fallback string) (string, error) {
	res, err := g.git(ctx, repo, "for-each-ref", "--format=%(refname)", "refs/heads/", "refs/remotes/")
	if err != nil {
		return "", err
	}
	candidates := append([]string{fallback}, strings.Fields(res.Stdout)...)
	best, count := fallback, int(^uint(0)>>1)
	for _, candidate := range candidates {
		short := strings.TrimPrefix(strings.TrimPrefix(candidate, "refs/heads/"), "refs/remotes/")
		if short == branch || candidate == target || strings.HasSuffix(candidate, "/HEAD") {
			continue
		}
		if strings.HasPrefix(candidate, "refs/remotes/") {
			_, remoteBranch, _ := strings.Cut(short, "/")
			if remoteBranch == branch {
				continue
			}
		}
		fork, err := g.MergeBase(ctx, repo, target, candidate)
		if err != nil {
			if tsexec.IsExit(err) {
				continue
			} // unrelated history has no fork
			return "", err
		}
		n, err := g.CountCommits(ctx, repo, fork, target)
		if err != nil {
			return "", err
		}
		// A branch that already holds all of target's commits was cut from
		// it, or merged it: a child, not a parent. Only the default may win
		// that way, as it does once the work is merged.
		if n == 0 && candidate != fallback {
			continue
		}
		if n < count {
			best, count = short, n
		}
	}
	return best, nil
}
