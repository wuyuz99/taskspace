package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	osexec "os/exec"
	"slices"
	"strings"
	"time"

	"github.com/wuyuz99/taskspace/internal/runtime"
	"github.com/wuyuz99/taskspace/internal/task"
)

// StartOptions shape Start.
type StartOptions struct {
	// Root starts only the primary root window.
	Root bool
	// Repos limits Start to these repositories, without the root window.
	Repos []string
	// NoRuntime creates the worktrees and stops there.
	NoRuntime bool
	// Fresh launches every window with its command, never its resume
	// command; see Start.
	Fresh bool
}

// StartResult is what Start brought up.
type StartResult struct {
	// Task is the record as Start left it, after its hooks.
	Task *task.Task
	// Session is the task's session, up unless NoRuntime.
	Session string
}

// Start creates the task's missing worktrees, running each new one's
// post_create hook, and brings up its session with the windows that are not
// there yet, saved extra windows included.
//
// A window it brings back resumes its agent's conversation, with the
// window's resume command, when it has one and its agent has run in that
// directory before: a worktree just created, or one only --no-runtime made,
// holds no conversation. One whose resume command exits at once is
// relaunched fresh. opts.Fresh never resumes. It is safe to repeat: existing
// worktrees and windows, dead ones included, are left alone. It releases the task's lock before it returns,
// so a caller may attach to the session, unless ctx came holding it already.
func (s *Service) Start(ctx context.Context, name string, opts StartOptions) (*StartResult, error) {
	t, ctx, unlock, err := s.Lock(ctx, name, s.command("tspace start"), true)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if opts.Root && (len(opts.Repos) > 0 || !t.HasRootWindow()) {
		return nil, fmt.Errorf("--root requires a task with a root window and no repository arguments")
	}
	names, err := selectRepos(t, opts.Repos)
	if err != nil {
		return nil, err
	}
	if opts.Root {
		names = nil
	}
	if len(names) == 0 && !opts.Root {
		return nil, fmt.Errorf("task %q has no repositories; add one with: tspace repo add %s REPO", t.Name, t.Name)
	}

	created := map[string]bool{}
	bases := map[string]string{}
	hooked := false
	for _, name := range names {
		// What an earlier repository's hook changed applies to this one.
		if hooked {
			if t, err = s.Store.Load(t.Name); err != nil {
				return nil, err
			}
		}
		r, ok := t.Repos[name]
		if !ok {
			return nil, fmt.Errorf("%s was removed from %s while it started", name, t.Name)
		}
		res, err := s.Git.EnsureWorktree(ctx, r.Path, r.Worktree, r.Branch, r.Base)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		s.report(WorktreeReady{Repo: name, Branch: r.Branch, Result: res})

		if res.Created {
			created[name] = true
			// Whatever ran in an earlier worktree here went with it.
			if err := s.Store.ForgetLaunched(t.Name, name); err != nil {
				s.warnf("tspace: %v", err)
			}
			hooked = hooked || r.PostCreate != ""
			if err := s.RunPostCreate(ctx, t, name, r); err != nil {
				// A half-provisioned worktree the user can inspect beats
				// destroying one, so this is a warning, not a rollback.
				s.report(HookFailed{Repo: name, Err: err})
			}
			if res.Base != "" && r.Base != res.Base {
				bases[name] = res.Base
			}
		}
	}

	// A post_create hook may have updated the record meanwhile, so the bases
	// resolved here go onto the latest one, not over it, and the windows are
	// made from that.
	switch {
	case len(bases) > 0:
		latest, err := s.Store.Update(ctx, t.Name, s.command("tspace start")+" "+t.Name, func(cur *task.Task) error {
			for name, base := range bases {
				if r, ok := cur.Repos[name]; ok {
					r.Base = base
					cur.SetRepo(name, r)
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		t = latest
	case hooked:
		if t, err = s.Store.Load(t.Name); err != nil {
			return nil, err
		}
	}
	result := &StartResult{Task: t, Session: s.Runtime.SessionName(t.Name)}
	// The root window comes first, so a fresh session opens on the agent that
	// can see every repository. Naming repositories starts only those. A
	// single-repo task has none: its agent runs in the repository.
	var windows []runtime.Window
	// fresh holds what a resumed window would run instead.
	fresh := map[string]runtime.Window{}
	resume := func(w runtime.Window) runtime.Window {
		if argv := t.ResumeFor(w.Name); len(argv) > 0 && !created[w.Name] && s.Store.Launched(t.Name, w.Name) && !opts.Fresh {
			fresh[w.Name] = w
			w.Command = ExpandArgv(s.Store, t, argv)
		}
		return w
	}
	withRoot := len(opts.Repos) == 0 && t.HasRootWindow()
	if withRoot {
		windows = append(windows, resume(RootWindow(s.Store, t)))
	}
	for _, name := range names {
		windows = append(windows, resume(RepoWindow(s.Store, t, name)))
	}
	// Saved windows go with what is started: those at the root with the whole
	// task, the rest with their repositories. The root window alone has none.
	atRoot := withRoot
	for _, w := range t.Windows {
		switch {
		case opts.Root:
			continue
		case w.Repo == "" && len(opts.Repos) == 0:
			atRoot = true
		case w.Repo == "" || !slices.Contains(names, w.Repo):
			continue
		}
		windows = append(windows, ExtraWindow(s.Store, t, w))
	}

	if opts.NoRuntime {
		return result, nil
	}
	if atRoot {
		// A multiplexer cannot open a window in a directory that does not exist.
		if err := os.MkdirAll(s.Store.TaskDir(t.Name), 0o755); err != nil {
			return nil, err
		}
	}
	s.report(Launching{Task: t, Root: withRoot, Repos: names})
	if err := s.Runtime.Available(ctx); err != nil {
		s.warnf("worktrees are ready, but the runtime is unavailable: %v", err)
		return nil, err
	}

	// Unreadable, the session counts as new: checkLaunched then waits, and no
	// signals are cleared on a guess.
	before, err := s.Runtime.State(ctx, result.Session)
	// Signals left by a session that is gone describe agents that are no
	// longer there.
	if err == nil && !before.Exists {
		if err := s.Store.ClearSignals(t.Name); err != nil {
			s.warnf("tspace: %v", err)
		}
	}
	if err := s.ensureContextDir(t); err != nil {
		return nil, err
	}
	// Only a window not already there is launched, resumed or not. Without
	// the session's state that is unknown, so nothing is taken as resumed.
	for name := range fresh {
		if _, open := before.Pane(s.Runtime.WindowName(name)); err != nil || (open && before.Exists) {
			delete(fresh, name)
		}
	}
	if err := s.Runtime.Start(ctx, result.Session, windows); err != nil {
		return nil, err
	}
	for _, w := range windows {
		if _, ok := fresh[w.Name]; ok {
			s.report(Resumed{Task: t.Name, Window: w.Name, Command: w.Command})
		}
		if !task.IsExtraWindow(w.Name) {
			if err := s.Store.MarkLaunched(t.Name, w.Name); err != nil {
				s.warnf("tspace: %v", err)
			}
		}
	}
	if _, err := s.checkLaunched(ctx, t, result.Session, before, windows, append([]string{task.RootWindowName}, names...), fresh); err != nil {
		return nil, err
	}
	// Only a session that survived its launch counts as running, so one
	// whose agents die at once is never offered as interrupted.
	if err := s.Store.MarkRunning(t.Name); err != nil {
		s.warnf("tspace: %v", err)
	}
	return result, nil
}

// StartInterrupted starts every interrupted task in turn, reporting
// StartingTask before each and TaskStarted or TaskNotStarted after it; a
// failure does not stop the rest. It returns the tasks that did not start.
func (s *Service) StartInterrupted(ctx context.Context, opts StartOptions) (started, failed []string, err error) {
	names, err := s.Interrupted(ctx)
	if err != nil {
		return nil, nil, err
	}
	for _, name := range names {
		s.report(StartingTask{Task: name})
		res, err := s.Start(ctx, name, opts)
		if err != nil {
			s.report(TaskNotStarted{Task: name, Err: err})
			failed = append(failed, name)
			continue
		}
		s.report(TaskStarted{Result: res})
		started = append(started, name)
	}
	return started, failed, nil
}

// Interrupted lists the tasks whose session something other than stop or
// clean ended, such as a computer restart, by name. A task whose session is
// up, or could not be checked, is not among them.
func (s *Service) Interrupted(ctx context.Context) ([]string, error) {
	tasks, err := s.Store.List()
	var skipped *task.SkippedError
	if err != nil && !errors.As(err, &skipped) {
		return nil, err
	}
	var names []string
	for _, t := range tasks {
		if !s.Store.Interrupted(t.Name) {
			continue
		}
		if running, err := s.Runtime.HasSession(ctx, s.Runtime.SessionName(t.Name)); err == nil && !running {
			names = append(names, t.Name)
		}
	}
	return names, nil
}

// checkLaunched reports what Start launched that is already gone. Starting
// returns once the windows exist, which says nothing about whether their
// commands survive the first second, and a session can go with them; names
// are the windows to look at, task.RootWindowName among them. A
// resumed window that is gone is relaunched once with fresh, its command:
// an agent asked to resume where it has no conversation may just exit.
func (s *Service) checkLaunched(ctx context.Context, t *task.Task, session string, before runtime.SessionState, windows []runtime.Window, names []string, fresh map[string]runtime.Window) (relaunched []string, err error) {
	launched := !before.Exists
	for _, w := range windows {
		if _, ok := before.Pane(s.Runtime.WindowName(w.Name)); !ok {
			launched = true
		}
	}
	if launched && s.Settle > 0 {
		select {
		case <-time.After(s.Settle):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	st, err := s.Runtime.State(ctx, session)
	if err != nil {
		s.warnf("could not inspect agent state: %v", err)
		return nil, nil
	}
	if !st.Exists {
		return nil, fmt.Errorf("session %s exited right after it started; check the agent commands with `tspace task show %s`, then run start again", session, t.Name)
	}
	var again []runtime.Window
	for _, name := range names {
		pane, ok := st.Pane(s.Runtime.WindowName(name))
		w, resumed := fresh[name]
		switch {
		case !ok && resumed:
			// A multiplexer that drops a window once its command exits.
			if err := s.Runtime.OpenWindow(ctx, session, w); err != nil {
				s.warnf("%s: its resume command exited at once, and starting it fresh failed: %v", windowLabel(name), err)
				continue
			}
			s.report(ResumeFailed{Task: t.Name, Window: name})
			again = append(again, w)
			relaunched = append(relaunched, name)
			continue
		case !ok || !pane.Dead:
			continue
		case !resumed:
			s.report(WindowExited{Task: t.Name, Window: name})
			continue
		}
		if err := s.Runtime.Restart(ctx, session, w); err != nil {
			s.warnf("%s: its resume command exited at once, and starting it fresh failed: %v", windowLabel(name), err)
			continue
		}
		s.report(ResumeFailed{Task: t.Name, Window: name})
		again = append(again, w)
		relaunched = append(relaunched, name)
	}
	if len(again) == 0 {
		return relaunched, nil
	}
	// Looked at as just launched, so it gets its second to fail in too.
	_, err = s.checkLaunched(ctx, t, session, runtime.SessionState{}, again, relaunched, nil)
	return relaunched, err
}

// windowLabel names a window in a sentence.
func windowLabel(name string) string {
	if name == task.RootWindowName {
		return "the root window"
	}
	return name
}

// RunPostCreate runs the repo's provisioning hook in its new worktree.
//
// A fresh worktree has no .env, node_modules or venv. Without this hook the
// tool is not usable on a real project on the first try.
//
// The hook gets TSPACE_REPO_PATH pointing at the source repository. Without it
// the only way to reach the original checkout is a relative path back out of
// the worktree, whose depth depends on where TSPACE_ROOT is — so such a hook
// silently breaks for anyone who moves their state directory. Prefer:
//
//	cp "$TSPACE_REPO_PATH/.env" .
func (s *Service) RunPostCreate(ctx context.Context, t *task.Task, name string, r task.Repo) error {
	if r.PostCreate == "" {
		return nil
	}
	s.report(HookStarted{Repo: name, Command: r.PostCreate})

	cmd := osexec.CommandContext(ctx, "sh", "-c", r.PostCreate)
	cmd.Dir = r.Worktree
	cmd.Stdout, cmd.Stderr = s.Stdout, s.Stderr
	cmd.Env = append(os.Environ(),
		EnvTask+"="+t.Name,
		EnvWindow+"=",
		task.EnvRoot+"="+s.Store.Root(),
		"TSPACE_REPO="+name,
		"TSPACE_REPO_PATH="+r.Path,
		"TSPACE_WORKTREE="+r.Worktree,
		"TSPACE_TASK_DIR="+s.Store.TaskDir(t.Name),
		"TSPACE_CONTEXT_DIR="+s.Store.ContextDir(t.Name),
	)
	// The hook runs while start holds the task, so a tspace it runs on the
	// task inherits the lock instead of waiting for start, which waits for it.
	if l := s.Store.HeldLock(ctx, t.Name); l != nil {
		cmd.Env = append(cmd.Env, l.Env())
	}
	return cmd.Run()
}

// selectRepos resolves optional repository names against a task.
func selectRepos(t *task.Task, names []string) ([]string, error) {
	if len(names) == 0 {
		return t.RepoNames(), nil
	}
	for _, name := range names {
		if _, ok := t.Repo(name); !ok {
			return nil, fmt.Errorf("task %q has no repo named %q (has: %s)",
				t.Name, name, strings.Join(t.RepoNames(), ", "))
		}
	}
	return names, nil
}
