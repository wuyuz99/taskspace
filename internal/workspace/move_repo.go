package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/wuyuz99/taskspace/internal/runtime"
	"github.com/wuyuz99/taskspace/internal/task"
)

// MoveRepo moves a repository from task old to task to, called name there,
// or what it is called now when name is empty. Its worktree goes along, with
// whatever is in it, and it keeps its branch, base, agent, resume command,
// setup and saved windows. Both tasks stay locked until the operation, or its
// rollback, is complete, so neither record is left claiming or missing it.
func (s *Service) MoveRepo(ctx context.Context, old, repo, to, name string) error {
	if name == "" {
		name = repo
	}
	for _, n := range []string{old, to} {
		if err := task.ValidateName("task", n); err != nil {
			return err
		}
	}
	if err := task.ValidateName("repo", name); err != nil {
		return err
	}
	if old == to {
		return fmt.Errorf("%s is already in task %s", repo, old)
	}
	// No lock file for a name that is not a task.
	for _, n := range []string{old, to} {
		if !s.Store.Exists(n) {
			return fmt.Errorf("%q: %w", n, task.ErrNotFound)
		}
	}
	ctx, unlock, err := s.acquireBoth(ctx, s.command("tspace repo move"), old, to)
	if err != nil {
		return err
	}
	defer unlock()
	src, err := s.Store.Load(old)
	if err != nil {
		return err
	}
	dst, err := s.Store.Load(to)
	if err != nil {
		return err
	}
	r, ok := src.Repo(repo)
	if !ok {
		return fmt.Errorf("task %q has no repo named %q (has: %s)", old, repo, strings.Join(src.RepoNames(), ", "))
	}
	if existing, ok := dst.Repo(name); ok {
		return fmt.Errorf("task %q already has a repo named %q (%s); use --name to pick another", to, name, existing.Path)
	}
	for _, other := range dst.RepoNames() {
		if sameDirectory(dst.Repos[other].Path, r.Path) {
			return fmt.Errorf("%s is already attached to task %q as %q", r.Path, to, other)
		}
	}
	var windows []task.Window
	for _, w := range src.Windows {
		if w.Repo != repo {
			continue
		}
		if _, taken := dst.SavedWindow(w.Name); taken {
			return fmt.Errorf("task %q already has a saved window %s, which %s would bring along", to, w.SessionName(), repo)
		}
		w.Repo = name
		windows = append(windows, w)
	}
	if err := s.checkMoveWindows(ctx, src, dst, repo, name, windows); err != nil {
		return err
	}

	from, dest := r.Worktree, s.Store.WorktreePath(to, name)
	if expected := s.Store.WorktreePath(old, repo); filepath.Clean(from) != filepath.Clean(expected) {
		return fmt.Errorf("%s's worktree is outside its expected path %s", repo, expected)
	}
	if _, err := os.Lstat(dest); err == nil {
		return fmt.Errorf("%s already exists; it is where %s's worktree goes", dest, name)
	} else if !os.IsNotExist(err) {
		return err
	}
	present, err := s.movableWorktree(ctx, r.Path, from)
	if err != nil {
		return err
	}
	if cwd, err := os.Getwd(); err == nil && present && pathInside(cwd, from) {
		return fmt.Errorf("run repo move from outside %s, which it moves", from)
	}
	// Migrate legacy notes before beginning the transaction, so that putting
	// a record back cannot create files outside the undo log.
	for _, t := range []*task.Task{src, dst} {
		if t.Outdated() {
			if err := s.Store.Save(t); err != nil {
				return err
			}
		}
	}
	previous, err := s.Store.Load(to)
	if err != nil {
		return err
	}

	// Without a carrier, say before anything moves what stays behind.
	carriers := s.carriers(s.conversationConfig())
	var resumed []movedDir
	for _, d := range s.resumedByDirectory(src, s.Store.TaskDir(old)) {
		if d.window == repo {
			resumed = append(resumed, d)
		}
	}
	if len(carriers) == 0 {
		s.warnConversationsLeft(old, resumed, nil)
	}
	var moved []movedDir
	if present {
		moved = append(moved, newMovedDir(repo, from, dest))
	}
	// What Launched says goes with the agent that ran, once its conversation
	// is there to resume. Without an agent of its own the window ran old's,
	// or a shell, and to's agent has run nothing.
	launched := len(r.Agent) > 0 && s.Store.Launched(old, repo)
	leftAgent := repo == src.AgentRepo && len(r.Agent) == 0 && len(src.Agent) > 0

	var undo undoLog
	if present {
		root := s.Store.TaskDir(to)
		if _, err := os.Stat(root); os.IsNotExist(err) {
			if err := os.MkdirAll(root, 0o755); err != nil {
				return err
			}
			undo = append(undo, func() error { return os.Remove(root) })
		}
		if err := s.Git.MoveWorktree(ctx, r.Path, from, dest); err != nil {
			return undo.rollback(err)
		}
		// Rollback ignores cancellation so an interrupted command can restore paths.
		backCtx := context.WithoutCancel(ctx)
		undo = append(undo, func() error { return s.Git.MoveWorktree(backCtx, r.Path, dest, from) })
	}
	before := *src
	src.RemoveRepo(repo)
	first := len(dst.Repos) == 0
	r.Worktree = dest
	dst.SetRepo(name, r)
	dst.Windows = append(dst.Windows, windows...)
	openRoot := s.SettleLayout(ctx, dst, first)
	if err := ctx.Err(); err != nil {
		return undo.rollback(err)
	}
	// Claimed by both for a moment, never by neither.
	if err := s.Store.Save(dst); err != nil {
		return undo.rollback(err)
	}
	undo = append(undo, func() error { return s.Store.Save(previous) })
	if err := s.Store.Save(src); err != nil {
		return undo.rollback(err)
	}

	// Done: nothing from here on undoes the move.
	s.noteAgentToRoot(&before, repo)
	if leftAgent && dst.AgentRepo != name {
		s.warnf("note: %s ran %s's agent, which stays with that task; in %s its window runs a shell, until tspace repo update %s %s --agent CMD gives it one", name, old, to, to, name)
	}
	if openRoot {
		s.OpenRootWindow(ctx, dst)
	}
	// History follows only a move that happened, and never undoes it.
	carried := map[string]bool{}
	if len(carriers) > 0 {
		carried = s.carryConversations(old, carriers, moved)
		s.warnConversationsLeft(old, resumed, carried)
	}
	s.moveRunData(old, repo, to, name, launched && carried[repo], windows)
	return nil
}

// checkMoveWindows refuses a move whose windows are open, or may be, in
// either task's session. A session that cannot be checked is an error.
func (s *Service) checkMoveWindows(ctx context.Context, src, dst *task.Task, repo, name string, windows []task.Window) error {
	up, err := s.SessionUp(ctx, src.Name)
	if err != nil {
		return err
	}
	if up {
		// The agent moves to the root window, so it must not be running in
		// this one, nor might be.
		if repo == src.AgentRepo {
			return fmt.Errorf("the task's agent runs in %s's window; stop the task first: tspace stop %s", repo, src.Name)
		}
		st, err := s.Runtime.State(ctx, s.Runtime.SessionName(src.Name))
		if err != nil {
			return err
		}
		if w := s.unsavedWindow(src, st); w != "" {
			return fmt.Errorf("window %s has no saved repository and may be working in %s; close it, or stop the task first: tspace stop %s", w, repo, src.Name)
		}
		if w := s.openWindow(st, repo, windows); w != "" {
			return fmt.Errorf("%s is open in %s; close it first: tspace stop %s %s", w, src.Name, src.Name, w)
		}
	}
	up, err = s.SessionUp(ctx, dst.Name)
	if err != nil || !up {
		return err
	}
	st, err := s.Runtime.State(ctx, s.Runtime.SessionName(dst.Name))
	if err != nil {
		return err
	}
	if w := s.openWindow(st, name, windows); w != "" {
		return fmt.Errorf("%s already has a window %s; close it first", dst.Name, w)
	}
	return nil
}

// openWindow names the first of repo's window and windows that is open in st,
// or "" if none is.
func (s *Service) openWindow(st runtime.SessionState, repo string, windows []task.Window) string {
	names := []string{repo}
	for _, w := range windows {
		names = append(names, w.SessionName())
	}
	for _, n := range names {
		if _, ok := st.Pane(s.Runtime.WindowName(n)); ok {
			return n
		}
	}
	return ""
}

// moveRunData brings what tspace remembers about the moved windows up to
// date. It is session memory, so a failure is a warning.
func (s *Service) moveRunData(old, repo, to, name string, launched bool, windows []task.Window) {
	warn := func(err error) {
		if err != nil {
			s.warnf("tspace: %v", err)
		}
	}
	warn(s.Store.ForgetLaunched(old, repo))
	if launched {
		warn(s.Store.MarkLaunched(to, name))
	} else {
		warn(s.Store.ForgetLaunched(to, name))
	}
	warn(s.Store.ClearSignal(old, repo))
	for _, w := range windows {
		warn(s.Store.ClearSignal(old, w.SessionName()))
	}
}
