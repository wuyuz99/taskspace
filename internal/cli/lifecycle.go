package cli

import (
	"context"
	"fmt"
	"os"
	osexec "os/exec"

	"github.com/aw-tool/aw/internal/runtime"
	"github.com/aw-tool/aw/internal/task"
	"github.com/spf13/cobra"
)

func (a *App) newStartCmd() *cobra.Command {
	var noRuntime bool

	cmd := &cobra.Command{
		Use:   "start TASK [REPO...]",
		Short: "Create the task's worktrees and launch its agents",
		Long: "start creates any missing worktrees and brings up the task's session.\n" +
			"It is safe to re-run: existing worktrees are reused and running agents\n" +
			"are left alone.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			t, err := a.loadTask(args[0])
			if err != nil {
				return err
			}
			names, err := selectRepos(t, args[1:])
			if err != nil {
				return err
			}
			if len(names) == 0 {
				return fmt.Errorf("task %q has no repositories; add one with: aw repo add %s PATH", t.Name, t.Name)
			}

			changed := false
			var windows []runtime.Window
			for _, name := range names {
				r := t.Repos[name]
				res, err := a.Git.EnsureWorktree(ctx, r.Path, r.Worktree, r.Branch, r.Base)
				if err != nil {
					return fmt.Errorf("%s: %w", name, err)
				}
				switch {
				case res.Created && res.Base != "":
					a.printf("%-12s created worktree on %s (from %s)\n", name, r.Branch, res.Base)
				case res.Created:
					a.printf("%-12s created worktree on existing branch %s\n", name, r.Branch)
				default:
					a.printf("%-12s worktree ready\n", name)
				}

				if res.Created {
					if err := a.runPostCreate(ctx, name, r); err != nil {
						// A half-provisioned worktree the user can inspect beats
						// destroying one, so this is a warning, not a rollback.
						a.warnf("%-12s post_create failed: %v\n", name, err)
					}
					if res.Base != "" && r.Base != res.Base {
						r.Base = res.Base
						t.SetRepo(name, r)
						changed = true
					}
				}

				windows = append(windows, runtime.Window{
					Name:    name,
					Dir:     r.Worktree,
					Command: r.Agent,
				})
			}

			if changed {
				if err := a.Store.Save(t); err != nil {
					return err
				}
			}

			if noRuntime {
				return nil
			}
			if err := a.Runtime.Available(ctx); err != nil {
				a.warnf("worktrees are ready, but the runtime is unavailable: %v\n", err)
				return err
			}

			session := a.Runtime.SessionName(t.Name)
			if err := a.Runtime.Start(ctx, session, windows); err != nil {
				return err
			}
			a.printf("\nsession %s is up; attach with: aw attach %s\n", session, t.Name)
			return nil
		},
	}
	cmd.Flags().BoolVar(&noRuntime, "no-runtime", false,
		"create worktrees but do not start the session")
	return cmd
}

// runPostCreate runs the repo's provisioning hook in its new worktree.
//
// A fresh worktree has no .env, node_modules or venv. Without this hook the
// tool is not usable on a real project on the first try.
func (a *App) runPostCreate(ctx context.Context, name string, r task.Repo) error {
	if r.PostCreate == "" {
		return nil
	}
	a.printf("%-12s post_create: %s\n", name, r.PostCreate)

	cmd := osexec.CommandContext(ctx, "sh", "-c", r.PostCreate)
	cmd.Dir = r.Worktree
	cmd.Stdout, cmd.Stderr = a.Out, a.Err
	return cmd.Run()
}

func (a *App) newStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop TASK",
		Short: "Stop the task's agents, leaving worktrees untouched",
		Long: "stop interrupts the agents, gives them a moment, then ends the session.\n" +
			"Worktrees and branches are not touched.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			t, err := a.loadTask(args[0])
			if err != nil {
				return err
			}
			session := a.Runtime.SessionName(t.Name)

			running, err := a.Runtime.HasSession(cmd.Context(), session)
			if err != nil {
				return err
			}
			if !running {
				a.printf("nothing running for %s\n", t.Name)
				return nil
			}
			if err := a.Runtime.Stop(cmd.Context(), session); err != nil {
				return err
			}
			a.printf("stopped %s; worktrees left in place\n", t.Name)
			return nil
		},
	}
}

func (a *App) newAttachCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "attach TASK",
		Short: "Attach to the task's session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			t, err := a.loadTask(args[0])
			if err != nil {
				return err
			}
			// Label the hosting tab before handing the terminal over, so the tab
			// is identifiable even after detaching.
			setTitle(a.Out, "aw: "+t.Name, a.noTitle)
			return a.Runtime.Attach(cmd.Context(), a.Runtime.SessionName(t.Name))
		},
	}
}

// selectRepos resolves optional repo-name arguments against a task.
func selectRepos(t *task.Task, args []string) ([]string, error) {
	if len(args) == 0 {
		return t.RepoNames(), nil
	}
	for _, name := range args {
		if _, ok := t.Repo(name); !ok {
			return nil, fmt.Errorf("task %q has no repo named %q (has: %s)",
				t.Name, name, joinNames(t.RepoNames()))
		}
	}
	return args, nil
}

// worktreeDir resolves TASK [REPO] to a single worktree path.
func (a *App) worktreeDir(t *task.Task, repoArg string) (task.Repo, string, error) {
	names := t.RepoNames()
	if len(names) == 0 {
		return task.Repo{}, "", fmt.Errorf("task %q has no repositories", t.Name)
	}

	name := repoArg
	if name == "" {
		if len(names) > 1 {
			return task.Repo{}, "", fmt.Errorf("task %q has several repos (%s); name one",
				t.Name, joinNames(names))
		}
		name = names[0]
	}
	r, ok := t.Repo(name)
	if !ok {
		return task.Repo{}, "", fmt.Errorf("task %q has no repo named %q (has: %s)",
			t.Name, name, joinNames(names))
	}
	if info, err := os.Stat(r.Worktree); err != nil || !info.IsDir() {
		return task.Repo{}, "", fmt.Errorf("worktree for %s is missing (%s); run: aw start %s",
			name, r.Worktree, t.Name)
	}
	return r, name, nil
}
