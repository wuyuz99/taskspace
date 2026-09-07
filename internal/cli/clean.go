package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	awgit "github.com/aw-tool/aw/internal/git"
	"github.com/aw-tool/aw/internal/task"
	"github.com/spf13/cobra"
)

// cleanPlan is what clean intends to do to one repository.
type cleanPlan struct {
	Name     string
	Repo     task.Repo
	Present  bool
	Blockers []string
	Discard  awgit.Discardability
}

func (a *App) newCleanCmd() *cobra.Command {
	var deleteBranch, force bool

	cmd := &cobra.Command{
		Use:   "clean TASK",
		Short: "Remove a task's worktrees, refusing if work could be lost",
		Long: "clean stops the task's session and removes its worktrees.\n\n" +
			"It refuses outright if any worktree has uncommitted changes, untracked\n" +
			"files or stashes: refusing is always better than losing code. Branches\n" +
			"are left alone unless --delete-branch, and even then only when their\n" +
			"work is merged into the base branch or pushed to an upstream.\n\n" +
			"The task record survives; use `aw task rm` to remove that.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			t, err := a.loadTask(args[0])
			if err != nil {
				return err
			}
			if len(t.Repos) == 0 {
				a.printf("task %s has no repositories\n", t.Name)
				return nil
			}

			plans, err := a.inspectForClean(ctx, t, deleteBranch)
			if err != nil {
				return err
			}

			if blocked := blockers(plans); len(blocked) > 0 && !force {
				return fmt.Errorf("refusing to clean %s; work would be lost:\n\n%s\n\n"+
					"commit, push or stash it, or re-run with --force to discard it",
					t.Name, strings.Join(blocked, "\n"))
			}

			// Stop the runtime only once the safety checks have passed, so a
			// refused clean does not disrupt running agents as a side effect.
			session := a.Runtime.SessionName(t.Name)
			if running, err := a.Runtime.HasSession(ctx, session); err == nil && running {
				if err := a.Runtime.Stop(ctx, session); err != nil {
					return err
				}
				a.printf("stopped session %s\n", session)
			}

			for _, p := range plans {
				if p.Present {
					if err := a.Git.RemoveWorktree(ctx, p.Repo.Path, p.Repo.Worktree, force); err != nil {
						return fmt.Errorf("%s: %w", p.Name, err)
					}
					a.printf("%-12s removed worktree\n", p.Name)
				} else {
					a.printf("%-12s no worktree\n", p.Name)
				}

				// Branch handling runs even when the worktree was already gone:
				// a repeated `clean --delete-branch` must still evaluate the
				// branch rather than silently skipping it.
				if !deleteBranch {
					continue
				}
				if !p.Discard.Safe() && !force {
					a.warnf("%-12s kept branch %s (%s)\n", p.Name, p.Repo.Branch, p.Discard.Reason())
					continue
				}
				if err := a.Git.DeleteBranch(ctx, p.Repo.Path, p.Repo.Branch, true); err != nil {
					a.warnf("%-12s could not delete branch %s: %v\n", p.Name, p.Repo.Branch, err)
					continue
				}
				a.printf("%-12s deleted branch %s (%s)\n", p.Name, p.Repo.Branch, p.Discard.Reason())
			}

			// Tidy the now-empty workspace directory, but never recursively:
			// this fails, deliberately, while the task's .context notes are
			// still there. Worktrees are disposable; the brief that describes
			// them is not, and only `aw task rm` deletes it.
			_ = os.Remove(a.Store.TaskDir(t.Name))

			a.printf("\ncleaned %s; the task record is still there (aw task rm %s to remove it)\n",
				t.Name, t.Name)
			return nil
		},
	}
	cmd.Flags().BoolVar(&deleteBranch, "delete-branch", false,
		"also delete task branches whose work is merged or pushed")
	cmd.Flags().BoolVar(&force, "force", false,
		"discard uncommitted work and unmerged branches")
	return cmd
}

// inspectForClean gathers the full picture before anything is removed, so that
// a refusal can list every problem at once rather than failing halfway.
func (a *App) inspectForClean(ctx context.Context, t *task.Task, wantBranchInfo bool) ([]cleanPlan, error) {
	var plans []cleanPlan

	for _, name := range t.RepoNames() {
		r := t.Repos[name]
		p := cleanPlan{Name: name, Repo: r}

		st, err := a.Git.WorktreeStatus(ctx, r.Worktree)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		p.Present = st.Exists

		if st.Exists {
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
			if wt, err := a.Git.FindWorktree(ctx, r.Path, r.Worktree); err == nil && wt != nil && wt.Locked {
				reason := wt.LockReason
				if reason == "" {
					reason = "no reason given"
				}
				p.Blockers = append(p.Blockers,
					fmt.Sprintf("  %s: worktree is locked (%s)", name, reason))
			}
		}

		if wantBranchInfo {
			d, err := a.Git.CanDiscardBranch(ctx, r.Path, r.Branch, r.Base)
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
