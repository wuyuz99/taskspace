package workspace

import (
	"context"
	"fmt"
	"os"
	"strings"

	tsgit "github.com/wuyuz99/taskspace/internal/git"
	"github.com/wuyuz99/taskspace/internal/task"
)

// ForceAdvice is the part of a refusal that says how to override it. dash
// runs no --force of its own, so it names the command there instead.
const ForceAdvice = "re-run with --force"

// CleanOptions shape Clean.
type CleanOptions struct {
	// Repos limits cleanup to these repositories and their saved windows.
	Repos []string
	// DeleteBranch also deletes task branches whose work is merged or pushed.
	DeleteBranch bool
	// Force discards uncommitted work and unmerged branches.
	Force bool
	// Remove deletes the record and notes too once the worktrees are gone,
	// as Delete does.
	Remove bool
}

// Clean stops the task's session and removes its worktrees, refusing before
// it touches anything, the session included, if work could be lost. The
// record and notes survive unless opts.Remove.
func (s *Service) Clean(ctx context.Context, name string, opts CleanOptions) error {
	t, ctx, unlock, err := s.Lock(ctx, name, s.command("tspace clean"), true)
	if err != nil {
		return err
	}
	defer unlock()
	selected := t
	if len(opts.Repos) > 0 {
		if opts.Remove {
			return fmt.Errorf("--rm cannot be combined with individual repositories")
		}
		only := *t
		only.Repos = map[string]task.Repo{}
		for _, name := range opts.Repos {
			r, ok := t.Repo(name)
			if !ok {
				return fmt.Errorf("task %q has no repo named %q (has: %s)", t.Name, name, strings.Join(t.RepoNames(), ", "))
			}
			only.Repos[name] = r
		}
		selected = &only
	}
	if len(t.Repos) == 0 {
		s.report(NoRepositories{Task: t.Name})
		if !opts.Remove {
			return nil
		}
		if _, err := s.stopForClean(ctx, t); err != nil {
			return fmt.Errorf("%w; the record of %s is kept", err, t.Name)
		}
		return s.finish(t)
	}

	// The whole plan is made under the lock it is carried out under, so
	// nothing changes in between.
	plans, err := s.inspectForClean(ctx, selected, opts.DeleteBranch)
	if err != nil {
		return err
	}
	if blocked := blockers(plans); len(blocked) > 0 && !opts.Force {
		return fmt.Errorf("refusing to clean %s; work would be lost:\n\n%s\n\n"+
			"commit, push or stash it, or %s to discard it",
			t.Name, strings.Join(blocked, "\n"), ForceAdvice)
	}

	// Stop the runtime only once the safety checks have passed, so a refused
	// clean does not disrupt running agents as a side effect.
	var stopped bool
	stoppedWhat := "stopped " + t.Name
	if len(opts.Repos) > 0 {
		stoppedWhat = "closed the windows of " + strings.Join(selected.RepoNames(), ", ")
		if stopped, err = s.stopSelectedForClean(ctx, t, selected); err != nil {
			return fmt.Errorf("%w; the worktrees are left alone", err)
		}
	} else if stopped, err = s.stopForClean(ctx, t); err != nil {
		return fmt.Errorf("%w; the worktrees are left alone", err)
	}
	// An agent may have committed, or left changes, as it shut down; what is
	// removed is decided by what the worktrees hold now, not before the stop.
	if stopped {
		if plans, err = s.inspectForClean(ctx, selected, opts.DeleteBranch); err != nil {
			return err
		}
		if blocked := blockers(plans); len(blocked) > 0 && !opts.Force {
			return fmt.Errorf("%s, but kept the worktrees; work appeared as its agents stopped:\n\n%s\n\n"+
				"commit, push or stash it, or %s to discard it; tspace start %s brings the windows back",
				stoppedWhat, strings.Join(blocked, "\n"), ForceAdvice, t.Name)
		}
	}

	for _, p := range plans {
		if p.Present {
			if err := s.Git.RemoveWorktree(ctx, p.Repo.Path, p.Repo.Worktree, opts.Force); err != nil {
				return fmt.Errorf("%s: %w", p.Name, err)
			}
			s.report(WorktreeRemoved{Repo: p.Name})
		} else {
			s.report(NoWorktree{Repo: p.Name})
		}

		// Branch handling runs even when the worktree was already gone: a
		// repeated `clean --delete-branch` must still evaluate the branch
		// rather than silently skipping it.
		if !opts.DeleteBranch || p.Discard.Tip == "" {
			continue
		}
		if !p.Discard.Safe() && !opts.Force {
			s.report(BranchKept{Repo: p.Name, Branch: p.Repo.Branch, Reason: p.Discard.Reason(), Path: p.Repo.Path, Orphaned: opts.Remove})
			continue
		}
		// Only the commit found safe goes; a branch that moved since is kept.
		if err := s.Git.DeleteBranchAt(ctx, p.Repo.Path, p.Repo.Branch, p.Discard.Tip); err != nil {
			s.report(BranchNotDeleted{Repo: p.Name, Branch: p.Repo.Branch, Err: err})
			continue
		}
		s.report(BranchDeleted{Repo: p.Name, Branch: p.Repo.Branch, Reason: p.Discard.Reason()})
	}

	// The rest of the task keeps running, so only the cleaned windows'
	// signals go.
	if len(opts.Repos) > 0 {
		for _, name := range selected.RepoNames() {
			_ = s.Store.ClearSignal(t.Name, name)
		}
		for _, w := range t.Windows {
			if _, ok := selected.Repos[w.Repo]; ok {
				_ = s.Store.ClearSignal(t.Name, w.SessionName())
			}
		}
		s.report(Cleaned{Task: t.Name, Repos: selected.RepoNames()})
		return nil
	}

	// The editor workspace lists the worktrees just removed, and is written
	// again by tspace editor; it must not keep the directory alive.
	_ = os.Remove(s.Store.CodeWorkspacePath(t.Name))
	if err := s.Store.ClearSignals(t.Name); err != nil {
		s.warnf("tspace: %v", err)
	}

	// Tidy the now-empty workspace directory, but never recursively: this
	// fails, deliberately, while the task's .context notes are still there.
	// Worktrees are disposable; the brief that describes them is not, and only
	// task rm or an explicit Remove deletes it.
	_ = os.Remove(s.Store.TaskDir(t.Name))

	s.report(Cleaned{Task: t.Name, Removing: opts.Remove})
	if opts.Remove {
		return s.finish(t)
	}
	return nil
}

// stopForClean ends the task's session if it is running. A session it cannot
// check might be running, and its worktrees must not go from under it, so
// that refuses. It reports whether it stopped one.
func (s *Service) stopForClean(ctx context.Context, t *task.Task) (stopped bool, err error) {
	running, err := s.SessionUp(ctx, t.Name)
	if err != nil {
		return false, err
	}
	if running {
		session := s.Runtime.SessionName(t.Name)
		if err := s.Runtime.Stop(ctx, session); err != nil {
			return false, fmt.Errorf("could not stop %s: %w", session, err)
		}
		s.report(SessionStopped{Session: session})
		stopped = true
	}
	s.markStopped(t.Name)
	return stopped, nil
}

// finish deletes a cleaned task's record and notes, but only if nothing else
// is left in its directory: a worktree repo rm left behind is work the record
// is the last pointer to.
func (s *Service) finish(t *task.Task) error {
	entries, err := os.ReadDir(s.Store.TaskDir(t.Name))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	var left []string
	for _, e := range entries {
		if e.Name() != ".context" {
			left = append(left, e.Name())
		}
	}
	if len(left) > 0 {
		return fmt.Errorf("kept the record of %s: %s still holds %s, which clean did not create; remove it, then run: tspace task rm %s",
			t.Name, s.Store.TaskDir(t.Name), strings.Join(left, ", "), t.Name)
	}
	return s.Delete(t)
}

// Delete removes a task whose worktrees are gone: its record and its notes.
// The caller holds the task's lock.
func (s *Service) Delete(t *task.Task) error {
	if err := s.DeleteRecord(t); err != nil {
		return err
	}
	held, err := s.Store.RemoveNotes(t.Name)
	if err != nil {
		return err
	}
	if len(held) > 0 {
		s.report(NotesRemoved{Task: t.Name, Files: held})
	}
	return nil
}

// DeleteRecord removes the task's record alone. The caller holds the task's
// lock.
func (s *Service) DeleteRecord(t *task.Task) error {
	if err := s.Store.Delete(t.Name); err != nil {
		return err
	}
	s.report(RecordRemoved{Task: t.Name})
	return nil
}

// cleanPlan is what Clean intends to do to one repository.
type cleanPlan struct {
	Name     string
	Repo     task.Repo
	Present  bool
	Blockers []string
	Discard  tsgit.Discardability
}

// inspectForClean gathers the full picture before anything is removed, so
// that a refusal can list every problem at once rather than failing halfway.
func (s *Service) inspectForClean(ctx context.Context, t *task.Task, wantBranchInfo bool) ([]cleanPlan, error) {
	var plans []cleanPlan

	for _, name := range t.RepoNames() {
		r := t.Repos[name]
		p := cleanPlan{Name: name, Repo: r}

		st, err := s.Git.WorktreeStatus(ctx, r.Worktree)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		p.Present = st.Exists

		if st.Exists {
			if st.Branch == "" {
				excludedBranch := ""
				if wantBranchInfo {
					excludedBranch = r.Branch
				}
				safe, err := s.Git.HeadReferenced(ctx, r.Worktree, excludedBranch)
				if err != nil {
					return nil, fmt.Errorf("%s: %w", name, err)
				}
				if !safe {
					p.Blockers = append(p.Blockers, fmt.Sprintf("  %s: detached HEAD has commits not protected by a surviving branch or tag; create a branch at HEAD before cleaning", name))
				}
			}
			if n := st.DirtyCount(); n > 0 {
				p.Blockers = append(p.Blockers,
					fmt.Sprintf("  %s: %d uncommitted change%s in %s", name, n, plural(n), r.Worktree))
			}
			if st.Stashes > 0 {
				p.Blockers = append(p.Blockers,
					fmt.Sprintf("  %s: %d stash%s on %s", name, st.Stashes, stashPlural(st.Stashes), r.Branch))
			}
			// A locked worktree is git's way of saying "someone meant this to
			// stay"; report it rather than surfacing a raw git failure later.
			if wt, err := s.Git.FindWorktree(ctx, r.Path, r.Worktree); err == nil && wt != nil && wt.Locked {
				reason := wt.LockReason
				if reason == "" {
					reason = "no reason given"
				}
				p.Blockers = append(p.Blockers,
					fmt.Sprintf("  %s: worktree is locked (%s)", name, reason))
			}
		}

		if wantBranchInfo {
			d, err := s.Git.CanDiscardBranch(ctx, r.Path, r.Branch, r.Base)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", name, err)
			}
			p.Discard = d
		}
		plans = append(plans, p)
	}
	return plans, nil
}

func blockers(plans []cleanPlan) []string {
	var out []string
	for _, p := range plans {
		out = append(out, p.Blockers...)
	}
	return out
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func stashPlural(n int) string {
	if n == 1 {
		return ""
	}
	return "es"
}
