package git

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Status is the observable git state of a worktree.
type Status struct {
	Exists     bool
	Branch     string
	Upstream   string
	Ahead      int
	Behind     int
	Staged     int
	Modified   int
	Untracked  int
	Conflicted int
	Stashes    int
}

// DirtyCount is the number of paths that would be lost by removing the
// worktree. Conflicted paths count once even though they appear in both the
// index and the worktree.
func (s Status) DirtyCount() int {
	return s.Staged + s.Modified + s.Untracked + s.Conflicted
}

// Dirty reports whether the worktree holds work that is not committed.
//
// Stashes count. They are stored in the repository's shared ref namespace
// rather than per-worktree, but a stash attributed to this worktree's branch is
// still work the user would lose sight of if the branch went away.
func (s Status) Dirty() bool {
	return s.DirtyCount() > 0 || s.Stashes > 0
}

// WorktreeStatus inspects a worktree directory.
//
// A missing directory is reported as Exists=false rather than an error: a task
// whose worktree has been deleted out from under it is a state tspace must be able
// to display, not one it should fail on.
func (g *Git) WorktreeStatus(ctx context.Context, worktree string) (Status, error) {
	var s Status

	if info, err := os.Stat(worktree); err != nil || !info.IsDir() {
		return s, nil
	}
	s.Exists = true

	res, err := g.git(ctx, worktree, "status", "--porcelain=v2", "--branch", "--untracked-files=normal")
	if err != nil {
		return s, fmt.Errorf("status of %s: %w", worktree, err)
	}
	parseStatusV2(res.Stdout, &s)

	if s.Branch != "" {
		n, err := g.stashCount(ctx, worktree, s.Branch)
		if err != nil {
			return s, err
		}
		s.Stashes = n
	}
	return s, nil
}

// parseStatusV2 reads `git status --porcelain=v2 --branch` output.
func parseStatusV2(out string, s *Status) {
	for _, raw := range strings.Split(out, "\n") {
		line := strings.TrimRight(raw, "\r")
		if line == "" {
			continue
		}
		switch {
		case strings.HasPrefix(line, "# branch.head "):
			head := strings.TrimPrefix(line, "# branch.head ")
			if head != "(detached)" {
				s.Branch = head
			}
		case strings.HasPrefix(line, "# branch.upstream "):
			s.Upstream = strings.TrimPrefix(line, "# branch.upstream ")
		case strings.HasPrefix(line, "# branch.ab "):
			s.Ahead, s.Behind = parseAheadBehind(strings.TrimPrefix(line, "# branch.ab "))
		case strings.HasPrefix(line, "# "):
			// other headers are not interesting
		case strings.HasPrefix(line, "? "):
			s.Untracked++
		case strings.HasPrefix(line, "u "):
			s.Conflicted++
		case strings.HasPrefix(line, "1 "), strings.HasPrefix(line, "2 "):
			// "<type> <XY> ..." where X is the index status and Y the worktree
			// status; '.' means unmodified.
			fields := strings.Fields(line)
			if len(fields) < 2 || len(fields[1]) < 2 {
				continue
			}
			if fields[1][0] != '.' {
				s.Staged++
			}
			if fields[1][1] != '.' {
				s.Modified++
			}
		}
	}
}

// parseAheadBehind reads the "+N -M" form of branch.ab.
func parseAheadBehind(v string) (ahead, behind int) {
	for _, f := range strings.Fields(v) {
		if len(f) < 2 {
			continue
		}
		n, err := strconv.Atoi(f[1:])
		if err != nil {
			continue
		}
		switch f[0] {
		case '+':
			ahead = n
		case '-':
			behind = n
		}
	}
	return ahead, behind
}

// stashCount counts stashes belonging to a branch.
//
// git keeps a single stash stack per repository, shared by every worktree, so
// there is no exact way to attribute a stash to a worktree. The stash subject
// does record the branch it was made on ("WIP on <branch>: ..." or
// "On <branch>: ..."), and tspace gives every task branch a unique name, so
// matching on the branch is reliable in practice. It is a heuristic; it errs
// toward reporting a stash, which is the safe direction.
func (g *Git) stashCount(ctx context.Context, dir, branch string) (int, error) {
	res, err := g.git(ctx, dir, "stash", "list", "--format=%gs")
	if err != nil {
		// A repository that has never stashed may not have refs/stash at all.
		return 0, nil
	}
	return countStashesForBranch(res.Stdout, branch), nil
}

func countStashesForBranch(out, branch string) int {
	if branch == "" {
		return 0
	}
	onWIP := "WIP on " + branch + ":"
	onPlain := "On " + branch + ":"
	n := 0
	for _, raw := range strings.Split(out, "\n") {
		line := strings.TrimSpace(strings.TrimRight(raw, "\r"))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, onWIP) || strings.HasPrefix(line, onPlain) {
			n++
		}
	}
	return n
}
