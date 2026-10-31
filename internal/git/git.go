// Package git wraps the git commands tspace needs.
//
// It never formats output for humans and never knows about tmux or the CLI.
// Errors carry git's own stderr and the argv that produced it, because a tool
// that paraphrases git makes problems harder to diagnose, not easier.
package git

import (
	"context"
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	tsexec "github.com/wuyuz99/taskspace/internal/exec"
)

// Git runs git commands through a Runner.
type Git struct {
	run tsexec.Runner
}

// New returns a Git using the given runner.
func New(r tsexec.Runner) *Git {
	if r == nil {
		r = tsexec.Real()
	}
	return &Git{run: r}
}

func (g *Git) git(ctx context.Context, dir string, args ...string) (tsexec.Result, error) {
	ctx = tsexec.WithEnv(ctx, RepositoryEnv(os.Environ()))
	return g.run.Run(ctx, dir, append([]string{"git"}, args...)...)
}

// RepositoryEnv clears Git's repository-local context (git rev-parse
// --local-env-vars), as required when a hook operates on another repository.
// SSH/askpass settings and global/system config file locations survive.
// GIT_CONFIG_COUNT and GIT_CONFIG_PARAMETERS are dropped, including any
// authentication supplied through them; see issue #132. Hook editor, author
// and quarantine context are also cleared, even when the user exported them:
// nothing distinguishes the two, and a persistent server would keep them.
func RepositoryEnv(env []string) []string {
	out := make([]string, 0, len(env))
	keys := RepositoryEnvVars()
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		if runtime.GOOS == "windows" {
			key = strings.ToUpper(key)
		}
		if slices.Contains(keys, key) {
			continue
		}
		out = append(out, entry)
	}
	return out
}

// RepositoryEnvVars returns Git's repository-local and hook-context variables.
// Launchers must unset these even when only a persistent server has them.
func RepositoryEnvVars() []string {
	return []string{
		"GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_CONFIG", "GIT_CONFIG_PARAMETERS",
		"GIT_CONFIG_COUNT", "GIT_OBJECT_DIRECTORY", "GIT_DIR", "GIT_WORK_TREE",
		"GIT_IMPLICIT_WORK_TREE", "GIT_GRAFT_FILE", "GIT_INDEX_FILE",
		"GIT_NO_REPLACE_OBJECTS", "GIT_REPLACE_REF_BASE", "GIT_PREFIX",
		"GIT_SHALLOW_FILE", "GIT_COMMON_DIR", "GIT_EDITOR", "GIT_SEQUENCE_EDITOR",
		"GIT_QUARANTINE_PATH", "GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_AUTHOR_DATE",
	}
}

// ok runs a git command and reports whether it exited zero, discarding the
// distinction between "ran and failed" and "output we do not care about".
// A failure to start the process is still returned as an error.
func (g *Git) ok(ctx context.Context, dir string, args ...string) (bool, error) {
	_, err := g.git(ctx, dir, args...)
	if err == nil {
		return true, nil
	}
	if tsexec.IsExit(err) {
		return false, nil
	}
	return false, err
}

// Available reports whether git is usable.
func (g *Git) Available(ctx context.Context) error {
	if _, err := g.git(ctx, "", "--version"); err != nil {
		return fmt.Errorf("git is required but not usable: %w", err)
	}
	return nil
}

// Validate checks that path is a usable git repository and returns the
// canonical absolute path of its top level.
//
// "Usable" excludes a repository sitting in the middle of a rebase, merge,
// cherry-pick, revert or bisect: adding a worktree to one is legal but the
// user almost certainly did not mean to, and the resulting confusion is worse
// than an upfront refusal.
func (g *Git) Validate(ctx context.Context, path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve %q: %w", path, err)
	}
	info, err := os.Stat(abs)
	if os.IsNotExist(err) {
		return "", fmt.Errorf("%s: no such directory", abs)
	}
	if err != nil {
		return "", fmt.Errorf("stat %s: %w", abs, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s: not a directory", abs)
	}

	res, err := g.git(ctx, abs, "rev-parse", "--show-toplevel")
	if err != nil {
		if tsexec.IsExit(err) {
			return "", fmt.Errorf("%s: not a git repository", abs)
		}
		return "", err
	}
	top := res.Out()
	if top == "" {
		return "", fmt.Errorf("%s: not a git repository", abs)
	}
	if resolved, err := filepath.EvalSymlinks(top); err == nil {
		top = resolved
	}
	top = filepath.Clean(top)

	if op, err := g.InProgress(ctx, top); err != nil {
		return "", err
	} else if op != "" {
		return "", fmt.Errorf("%s: repository has an unfinished %s; resolve it first", top, op)
	}
	return top, nil
}

// InProgress returns the name of an unfinished git operation, or "".
func (g *Git) InProgress(ctx context.Context, repo string) (string, error) {
	res, err := g.git(ctx, repo, "rev-parse", "--absolute-git-dir")
	if err != nil {
		if tsexec.IsExit(err) {
			return "", nil
		}
		return "", err
	}
	gitDir := res.Out()
	if gitDir == "" {
		return "", nil
	}

	// Ordered so the more specific operations win over plain "rebase".
	markers := []struct{ path, name string }{
		{"rebase-merge/interactive", "interactive rebase"},
		{"rebase-merge", "rebase"},
		{"rebase-apply", "rebase"},
		{"MERGE_HEAD", "merge"},
		{"CHERRY_PICK_HEAD", "cherry-pick"},
		{"REVERT_HEAD", "revert"},
		{"BISECT_LOG", "bisect"},
	}
	for _, m := range markers {
		if _, err := os.Stat(filepath.Join(gitDir, m.path)); err == nil {
			return m.name, nil
		}
	}
	return "", nil
}

// ResolveBase determines the ref new task branches should fork from.
//
// Order: origin/HEAD, then a conventional local default branch, then the
// branch HEAD is on, or its commit when detached: never HEAD itself, which
// means something else in each worktree.
// This exists because branching from whatever HEAD happens to be silently
// inherits an unrelated feature branch — a correctness bug, not a UX wrinkle.
// Callers are expected to report the chosen ref to the user.
func (g *Git) ResolveBase(ctx context.Context, repo string) (string, error) {
	res, err := g.git(ctx, repo, "symbolic-ref", "--quiet", "refs/remotes/origin/HEAD")
	if err != nil && !tsexec.IsExit(err) {
		return "", err
	}
	if err == nil {
		if ref := strings.TrimPrefix(res.Out(), "refs/remotes/"); ref != "" && ref != res.Out() {
			return ref, nil
		}
	}

	for _, candidate := range []string{"main", "master"} {
		found, err := g.ok(ctx, repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+candidate)
		if err != nil {
			return "", err
		}
		if found {
			return candidate, nil
		}
	}

	// A repository with no commits has no usable HEAD to fork from.
	hasHead, err := g.ok(ctx, repo, "rev-parse", "--verify", "--quiet", "HEAD")
	if err != nil {
		return "", err
	}
	if !hasHead {
		return "", fmt.Errorf("%s: repository has no commits to branch from", repo)
	}
	if res, err := g.git(ctx, repo, "symbolic-ref", "--quiet", "--short", "HEAD"); err == nil {
		return res.Out(), nil
	} else if !tsexec.IsExit(err) {
		return "", err
	}
	res, err = g.git(ctx, repo, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return "", err
	}
	return res.Out(), nil
}

// RefExists reports whether a ref resolves.
func (g *Git) RefExists(ctx context.Context, repo, ref string) (bool, error) {
	return g.ok(ctx, repo, "rev-parse", "--verify", "--quiet", ref)
}

// ResolveRef turns a ref the user typed into one that resolves, or "".
//
// A ref that resolves as written is kept as written, so a local branch the user
// named is the one used. A bare branch name that exists only on a remote — the
// usual case for "branch off develop" in a clone that never checked develop
// out — becomes that remote-tracking ref, origin first.
func (g *Git) ResolveRef(ctx context.Context, repo, ref string) (string, error) {
	ok, err := g.RefExists(ctx, repo, ref)
	if err != nil {
		return "", err
	}
	if ok {
		return ref, nil
	}
	return g.RemoteBranch(ctx, repo, ref)
}

// CheckBranchName reports why name cannot be a branch, or nil if it can.
func (g *Git) CheckBranchName(ctx context.Context, name string) error {
	if _, err := g.git(ctx, "", "check-ref-format", "--branch", name); err != nil {
		if tsexec.IsExit(err) {
			return fmt.Errorf("%q is not a valid branch name", name)
		}
		return err
	}
	return nil
}

// Remotes lists the repository's configured remotes, sorted as git prints them.
func (g *Git) Remotes(ctx context.Context, repo string) ([]string, error) {
	res, err := g.git(ctx, repo, "remote")
	if err != nil {
		return nil, fmt.Errorf("list remotes of %s: %w", repo, err)
	}
	return strings.Fields(res.Stdout), nil
}

// RemoteBranch returns the remote-tracking ref for branch, such as
// "origin/feature/x", or "" when no remote has it.
//
// origin wins when it has the branch. Otherwise exactly one remote must: with
// two candidates, picking one would be a guess about whose work to build on.
func (g *Git) RemoteBranch(ctx context.Context, repo, branch string) (string, error) {
	remotes, err := g.Remotes(ctx, repo)
	if err != nil {
		return "", err
	}
	var found []string
	for _, r := range remotes {
		ok, err := g.ok(ctx, repo, "show-ref", "--verify", "--quiet", "refs/remotes/"+r+"/"+branch)
		if err != nil {
			return "", err
		}
		if !ok {
			continue
		}
		if r == "origin" {
			return r + "/" + branch, nil
		}
		found = append(found, r+"/"+branch)
	}
	switch len(found) {
	case 0:
		return "", nil
	case 1:
		return found[0], nil
	}
	return "", fmt.Errorf("branch %q exists on several remotes (%s); name one, such as %s",
		branch, strings.Join(found, ", "), found[0])
}

// FetchRemotes fetches the remotes a new branch could come from: origin, the
// remote that base belongs to, or the only remote there is. It returns the
// remotes it fetched and the first failure.
//
// Callers treat a failure as a warning. A worktree created from slightly stale
// refs is still correct git; refusing to work offline is not.
func (g *Git) FetchRemotes(ctx context.Context, repo, base string) ([]string, error) {
	remotes, err := g.Remotes(ctx, repo)
	if err != nil {
		return nil, err
	}
	var want []string
	for _, r := range remotes {
		if r == "origin" || strings.HasPrefix(base, r+"/") {
			want = append(want, r)
		}
	}
	if len(want) == 0 && len(remotes) == 1 {
		want = remotes
	}
	var first error
	for _, r := range want {
		if _, err := g.git(ctx, repo, "fetch", "--quiet", r); err != nil && first == nil {
			first = fmt.Errorf("fetch %s: %w", r, err)
		}
	}
	return want, first
}

// CountCommits counts the commits reachable from to but not from from.
func (g *Git) CountCommits(ctx context.Context, dir, from, to string) (int, error) {
	res, err := g.git(ctx, dir, "rev-list", "--count", from+".."+to)
	if err != nil {
		return 0, fmt.Errorf("count commits %s..%s: %w", from, to, err)
	}
	n := 0
	if _, err := fmt.Sscanf(res.Out(), "%d", &n); err != nil {
		return 0, fmt.Errorf("parse commit count %s..%s: %q", from, to, res.Out())
	}
	return n, nil
}

// BranchExists reports whether a local branch exists.
func (g *Git) BranchExists(ctx context.Context, repo, branch string) (bool, error) {
	return g.ok(ctx, repo, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
}

// DeleteBranchAt deletes a local branch only if it still points to tip, the
// commit a caller's safety check was about, so a commit made since is never
// lost with it. Like git branch -D, it refuses a branch checked out, rebased
// or bisected in a worktree, and drops the branch's configuration with it.
func (g *Git) DeleteBranchAt(ctx context.Context, repo, branch, tip string) error {
	if tip == "" {
		return fmt.Errorf("delete branch %s: branch not found", branch)
	}
	if use, err := g.branchInUse(ctx, repo, branch); err != nil {
		return fmt.Errorf("delete branch %s: %w", branch, err)
	} else if use != "" {
		return fmt.Errorf("delete branch %s: it is %s", branch, use)
	}
	if _, err := g.git(ctx, repo, "update-ref", "-d", "refs/heads/"+branch, tip); err != nil {
		if res, rerr := g.git(ctx, repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch); rerr == nil && res.Out() != tip {
			return fmt.Errorf("delete branch %s: it moved to %s after it was checked at %s", branch, short(res.Out()), short(tip))
		}
		return fmt.Errorf("delete branch %s: %w", branch, err)
	}
	// Exits non-zero when the branch had no configuration, which is fine.
	_, _ = g.git(ctx, repo, "config", "--remove-section", "branch."+branch)
	return nil
}

// branchInUse says where a worktree is using branch, or "" if none is: on
// it, or rebasing or bisecting it, which leaves the worktree detached.
func (g *Git) branchInUse(ctx context.Context, repo, branch string) (string, error) {
	list, err := g.Worktrees(ctx, repo)
	if err != nil {
		return "", err
	}
	for _, wt := range list {
		if wt.Branch == branch {
			return "checked out at " + wt.Path, nil
		}
		if !wt.Detached || wt.Prunable {
			continue
		}
		res, err := g.git(ctx, wt.Path, "rev-parse", "--absolute-git-dir")
		if err != nil {
			return "", err
		}
		for _, m := range []struct{ file, want, use string }{
			{"rebase-merge/head-name", "refs/heads/" + branch, "being rebased at "},
			{"rebase-apply/head-name", "refs/heads/" + branch, "being rebased at "},
			{"BISECT_START", branch, "being bisected at "},
		} {
			if b, err := os.ReadFile(filepath.Join(res.Out(), m.file)); err == nil && strings.TrimSpace(string(b)) == m.want {
				return m.use + wt.Path, nil
			}
		}
	}
	return "", nil
}

func short(oid string) string {
	if len(oid) > 12 {
		return oid[:12]
	}
	return oid
}

// RenameBranch renames a local branch, which may be checked out in a
// worktree; git carries its upstream and reflog over.
func (g *Git) RenameBranch(ctx context.Context, repo, from, to string) error {
	if _, err := g.git(ctx, repo, "branch", "-m", from, to); err != nil {
		return fmt.Errorf("rename branch %s to %s: %w", from, to, err)
	}
	return nil
}

// Commit resolves ref in repo to the commit it names.
func (g *Git) Commit(ctx context.Context, repo, ref string) (string, error) {
	res, err := g.git(ctx, repo, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if err != nil {
		if tsexec.IsExit(err) {
			return "", fmt.Errorf("%s is not a commit in %s", ref, repo)
		}
		return "", err
	}
	return res.Out(), nil
}

// MergeBase is the best common ancestor of two commits in repo.
func (g *Git) MergeBase(ctx context.Context, repo, a, b string) (string, error) {
	res, err := g.git(ctx, repo, "merge-base", a, b)
	if err != nil {
		return "", fmt.Errorf("merge-base of %s and %s: %w", a, b, err)
	}
	return res.Out(), nil
}

// Rebase moves the commits the branch checked out in worktree has beyond
// upstream onto onto. A rebase that stops, on a conflict, Ctrl-C or anything
// else, is aborted; undone reports whether that left the branch and worktree
// as they were, and the error says why it stopped.
func (g *Git) Rebase(ctx context.Context, worktree, onto, upstream string) (undone bool, err error) {
	_, err = g.git(ctx, worktree, "rebase", "--onto", onto, upstream)
	if err == nil {
		return false, nil
	}
	err = fmt.Errorf("rebase onto %s: %w", onto, err)
	// Undone even when ctx is what stopped it.
	bg := context.WithoutCancel(ctx)
	if op, opErr := g.InProgress(bg, worktree); opErr == nil && op == "" {
		return true, err // stopped before it began
	}
	if _, abortErr := g.git(bg, worktree, "rebase", "--abort"); abortErr != nil {
		return false, fmt.Errorf("%w; it could not be aborted either (%v), so finish or abort it in %s", err, abortErr, worktree)
	}
	return true, err
}

// StashCount is the number of stashes made on branch, in any worktree of
// repo: they share one stash list.
func (g *Git) StashCount(ctx context.Context, repo, branch string) (int, error) {
	return g.stashCount(ctx, repo, branch)
}

// Upstream is the branch branch tracks, such as "origin/main", or "".
func (g *Git) Upstream(ctx context.Context, repo, branch string) (string, error) {
	return g.upstreamOf(ctx, repo, branch)
}

// UnsetUpstream stops branch tracking anything.
func (g *Git) UnsetUpstream(ctx context.Context, repo, branch string) error {
	if _, err := g.git(ctx, repo, "branch", "--unset-upstream", branch); err != nil {
		return fmt.Errorf("stop %s tracking its upstream: %w", branch, err)
	}
	return nil
}

// FullRefName is the full name of the ref ref names, such as
// "refs/remotes/origin/main", or "" when it names a commit some other way:
// HEAD~1, a reflog entry, an object ID.
func (g *Git) FullRefName(ctx context.Context, repo, ref string) (string, error) {
	res, err := g.git(ctx, repo, "rev-parse", "--symbolic-full-name", ref)
	if err != nil {
		if tsexec.IsExit(err) {
			return "", nil
		}
		return "", err
	}
	return res.Out(), nil
}

// Clone clones url into dest, a directory that is missing or empty, with its
// remote named origin whatever clone.defaultRemoteName says. It runs on the
// terminal rather than through the runner, so git can show its progress and
// ask for credentials as it would for the user; out receives both. The error
// leaves url out, as it may hold a token.
func (g *Git) Clone(ctx context.Context, url, dest string, in io.Reader, out io.Writer) error {
	cmd := osexec.CommandContext(ctx, "git", "clone", "--origin", "origin", "--", url, dest)
	cmd.Env = RepositoryEnv(os.Environ())
	cmd.Stdin, cmd.Stdout, cmd.Stderr = in, out, out
	return cmd.Run()
}

// OriginURL is the URL configured for repo's origin, as written, before any
// url.insteadOf rewrite; "" when it has none.
func (g *Git) OriginURL(ctx context.Context, repo string) (string, error) {
	res, err := g.git(ctx, repo, "config", "--get", "remote.origin.url")
	if err != nil {
		if tsexec.IsExit(err) {
			return "", nil
		}
		return "", err
	}
	return res.Out(), nil
}
