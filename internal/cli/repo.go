package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/aw-tool/aw/internal/task"
	"github.com/spf13/cobra"
)

func (a *App) newRepoCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "repo",
		Short: "Attach, configure, and detach repositories",
	}
	cmd.AddCommand(a.newRepoAddCmd(), a.newRepoRmCmd(), a.newRepoUpdateCmd())
	return cmd
}

func (a *App) newRepoUpdateCmd() *cobra.Command {
	var agent []string
	var hook string
	var shell bool
	cmd := &cobra.Command{
		Use:   "update TASK REPO",
		Short: "Update saved agent and setup settings",
		Long:  "Update the saved command or post-create hook. Running windows are untouched. Agent settings apply when a window is created or explicitly restarted; hooks run only when a worktree is newly created.",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			setAgent, setHook := cmd.Flags().Changed("agent"), cmd.Flags().Changed("post-create")
			if setAgent && shell {
				return fmt.Errorf("choose either --agent or --shell")
			}
			if !setAgent && !setHook && !shell {
				return fmt.Errorf("specify --agent, --shell, or --post-create")
			}
			if setAgent && (len(agent) == 0 || strings.TrimSpace(agent[0]) == "") {
				return fmt.Errorf("agent command is empty; use --shell to restore the default shell")
			}
			t, err := a.loadTask(args[0])
			if err != nil {
				return err
			}
			r, ok := t.Repo(args[1])
			if !ok {
				return fmt.Errorf("task %q has no repo named %q", t.Name, args[1])
			}
			if setAgent {
				r.Agent = agent
			}
			if shell {
				r.Agent = nil
			}
			if setHook {
				r.PostCreate = hook
			}
			t.SetRepo(args[1], r)
			if err := a.Store.Save(t); err != nil {
				return err
			}
			a.printf("updated %s/%s\n", t.Name, args[1])
			if setAgent || shell {
				a.printf("  agent: %s (applies on window creation or explicit restart)\n", agentDisplay(r))
			}
			if setHook {
				a.printf("  post_create: %s (applies to newly created worktrees)\n", dash(hook))
			}
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&agent, "agent", nil, "replace agent argv; repeat for arguments")
	cmd.Flags().BoolVar(&shell, "shell", false, "clear the agent command and use the default shell")
	cmd.Flags().StringVar(&hook, "post-create", "", "replace the setup hook; an empty value clears it")
	return cmd
}

type addRepoOptions struct {
	Name       string
	Branch     string
	Base       string
	Agent      []string
	PostCreate string
}

func (a *App) newRepoAddCmd() *cobra.Command {
	var opts addRepoOptions

	cmd := &cobra.Command{
		Use:   "add TASK PATH",
		Short: "Attach a repository to a task",
		Long: "add registers a repository with a task. The repo name defaults to the\n" +
			"directory's base name; --name overrides it.\n\n" +
			"Nothing is created on disk here — `aw start` makes the worktrees.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			t, err := a.loadTask(args[0])
			if err != nil {
				return err
			}
			if err := a.addRepo(cmd.Context(), t, opts.Name, args[1], opts); err != nil {
				return err
			}
			return a.Store.Save(t)
		},
	}
	cmd.Flags().StringVar(&opts.Name, "name", "", "repo name (default: the directory's base name)")
	cmd.Flags().StringVar(&opts.Branch, "branch", "", "branch to use (default: agent/TASK)")
	cmd.Flags().StringVar(&opts.Base, "base", "", "ref to branch from (default: origin/HEAD, then the local default branch)")
	cmd.Flags().StringArrayVar(&opts.Agent, "agent", nil, "agent command to run in this worktree, repeat for arguments")
	cmd.Flags().StringVar(&opts.PostCreate, "post-create", "",
		"shell command run once in a new worktree, e.g. 'cp ../../.env . && pnpm install'")
	return cmd
}

// addRepo validates the repository and records it on the task.
func (a *App) addRepo(ctx context.Context, t *task.Task, name, path string, opts addRepoOptions) error {
	canonical, err := a.Git.Validate(ctx, expandHome(path))
	if err != nil {
		return err
	}

	if name == "" {
		name = filepath.Base(canonical)
	}
	if err := task.ValidateName("repo", name); err != nil {
		return err
	}
	if existing, ok := t.Repo(name); ok {
		return fmt.Errorf("task %q already has a repo named %q (%s); use --name to pick another",
			t.Name, name, existing.Path)
	}
	for _, other := range t.RepoNames() {
		if t.Repos[other].Path == canonical {
			return fmt.Errorf("%s is already attached to task %q as %q", canonical, t.Name, other)
		}
	}

	branch := opts.Branch
	if branch == "" {
		branch = task.DefaultBranch(t.Name)
	}

	// Resolve the base now so that `aw task show` can report it before anything
	// is created, and so a later start cannot silently pick a different one.
	base := opts.Base
	if base == "" {
		base, err = a.Git.ResolveBase(ctx, canonical)
		if err != nil {
			return err
		}
	}

	t.SetRepo(name, task.Repo{
		Path:       canonical,
		Branch:     branch,
		Base:       base,
		Worktree:   a.Store.WorktreePath(t.Name, name),
		Agent:      opts.Agent,
		PostCreate: opts.PostCreate,
	})

	a.printf("added %s to %s\n", name, t.Name)
	a.printf("  repo:     %s\n", canonical)
	a.printf("  branch:   %s (from %s)\n", branch, base)
	a.printf("  worktree: %s\n", a.Store.WorktreePath(t.Name, name))
	return nil
}

func (a *App) newRepoRmCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rm TASK REPO",
		Short: "Detach a repository from a task",
		Long: "rm forgets the repository. It does not remove the worktree — run\n" +
			"`aw clean TASK` for that, so the safety checks apply.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			t, err := a.loadTask(args[0])
			if err != nil {
				return err
			}
			r, ok := t.Repo(args[1])
			if !ok {
				return fmt.Errorf("task %q has no repo named %q", t.Name, args[1])
			}
			delete(t.Repos, args[1])
			if err := a.Store.Save(t); err != nil {
				return err
			}
			a.printf("removed %s from %s\n", args[1], t.Name)
			a.warnf("note: the worktree at %s was left in place\n", r.Worktree)
			return nil
		},
	}
}

// parseRepoSpec reads the name=path form used by `aw task create --repo`.
func parseRepoSpec(spec string) (name, path string, err error) {
	name, path, found := strings.Cut(spec, "=")
	if !found {
		// A bare path is fine; the name comes from the directory.
		return "", spec, nil
	}
	if name == "" || path == "" {
		return "", "", fmt.Errorf("invalid --repo %q: expected name=path", spec)
	}
	return name, path, nil
}
