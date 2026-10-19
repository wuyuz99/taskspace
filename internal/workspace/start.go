package workspace

import (
	"context"
	"fmt"
	"os"
	osexec "os/exec"
	"strings"
	"time"

	"github.com/wuyuz99/taskspace/internal/runtime"
	"github.com/wuyuz99/taskspace/internal/task"
)

// StartOptions shape Start.
type StartOptions struct {
	// Repos limits Start to these repositories, without the root window.
	Repos []string
	// NoRuntime creates the worktrees and stops there.
	NoRuntime bool
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
// there yet. It is safe to repeat: existing worktrees and windows, dead ones
// included, are left alone. It returns with the lock released, so a caller
// may attach to the session.
func (s *Service) Start(ctx context.Context, name string, opts StartOptions) (*StartResult, error) {
	t, ctx, unlock, err := s.Lock(ctx, name, "tspace start", true)
	if err != nil {
		return nil, err
	}
	defer unlock()
	names, err := selectRepos(t, opts.Repos)
	if err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("task %q has no repositories; add one with: tspace repo add %s REPO", t.Name, t.Name)
	}

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
		latest, err := s.Store.Update(ctx, t.Name, "tspace start "+t.Name, func(cur *task.Task) error {
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
	withRoot := len(opts.Repos) == 0 && t.HasRootWindow()
	if withRoot {
		windows = append(windows, s.RootWindow(t))
	}
	for _, name := range names {
		windows = append(windows, s.RepoWindow(t, name))
	}

	if opts.NoRuntime {
		return result, nil
	}
	if withRoot {
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
	if err := s.Runtime.Start(ctx, result.Session, windows); err != nil {
		return nil, err
	}
	if err := s.checkLaunched(ctx, t, result.Session, before, windows, names); err != nil {
		return nil, err
	}
	return result, nil
}

// checkLaunched reports what Start launched that is already gone. Starting
// returns once the windows exist, which says nothing about whether their
// commands survive the first second, and a session can go with them.
func (s *Service) checkLaunched(ctx context.Context, t *task.Task, session string, before runtime.SessionState, windows []runtime.Window, names []string) error {
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
			return ctx.Err()
		}
	}
	st, err := s.Runtime.State(ctx, session)
	if err != nil {
		s.warnf("could not inspect agent state: %v", err)
		return nil
	}
	if !st.Exists {
		return fmt.Errorf("session %s exited right after it started; check the agent commands with `tspace task show %s`, then run start again", session, t.Name)
	}
	if pane, ok := st.Pane(s.Runtime.WindowName(task.RootWindowName)); ok && pane.Dead {
		s.report(WindowExited{Task: t.Name, Window: task.RootWindowName})
	}
	for _, name := range names {
		if pane, ok := st.Pane(s.Runtime.WindowName(name)); ok && pane.Dead {
			s.report(WindowExited{Task: t.Name, Window: name})
		}
	}
	return nil
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
