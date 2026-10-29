package workspace

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// RemoveRepo takes a repository out of a task. It cleans the repository
// first, refusing as clean does when work could be lost, so its worktree and
// windows go with it; then it forgets the repository and the saved windows
// that open in it. keepWorktree only forgets it, leaving its files and
// windows under the user's control. It returns the worktree it left in
// place, if any, for the caller to mention after saying it is done.
func (s *Service) RemoveRepo(ctx context.Context, name, repo string, keepWorktree bool) (left string, err error) {
	t, ctx, unlock, err := s.Lock(ctx, name, s.command("tspace repo rm"), true)
	if err != nil {
		return "", err
	}
	defer unlock()
	r, ok := t.Repo(repo)
	if !ok {
		return "", fmt.Errorf("task %q has no repo named %q (has: %s)", t.Name, repo, strings.Join(t.RepoNames(), ", "))
	}
	if keepWorktree && repo == t.AgentRepo {
		// The agent moves to the root window, so it must not be running in
		// this one, nor might be.
		running, err := s.SessionUp(ctx, t.Name)
		if err != nil {
			return "", fmt.Errorf("the task's agent runs in %s's window, which may still be up (%w); stop the task first: tspace stop %s", repo, err, t.Name)
		}
		if running {
			return "", fmt.Errorf("the task's agent runs in %s's window; stop the task first: tspace stop %s", repo, t.Name)
		}
	}
	if !keepWorktree {
		// Under the lock already held, so nothing changes in between.
		if err := s.Clean(ctx, t.Name, CleanOptions{Repos: []string{repo}}); err != nil {
			return "", err
		}
		// What is saved is what clean left.
		if t, err = s.Store.Load(t.Name); err != nil {
			return "", err
		}
	}
	if repo == t.AgentRepo && len(t.Resume) > 0 {
		s.warnf("note: the task's agent moves to the root window, at %s; a resume command that finds its conversation by directory will not find the one from %s", s.Store.TaskDir(t.Name), repo)
	}
	for _, w := range t.RemoveRepo(repo) {
		s.warnf("note: forgot saved window %s, which opened in %s", w.SessionName(), repo)
	}
	if err := s.Store.Save(t); err != nil {
		return "", err
	}
	// The same name added again starts over.
	if err := s.Store.ForgetLaunched(t.Name, repo); err != nil {
		s.warnf("tspace: %v", err)
	}
	// After clean there is no worktree left to speak of.
	if _, err := os.Stat(r.Worktree); err == nil {
		return r.Worktree, nil
	}
	return "", nil
}
