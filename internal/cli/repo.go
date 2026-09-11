package cli

import (
	"context"
	"fmt"
	"os"
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
		Use:   "add TASK REPO",
		Short: "Attach a repository to a task",
		Long: "add registers a repository with a task. REPO is a name registered with\n" +
			"`aw config repo` or a path, optionally followed by a branch and a base:\n\n" +
			"  aw repo add PAY-142 backend                  new branch agent/PAY-142\n" +
			"  aw repo add PAY-142 frontend@feature/x       an existing branch\n" +
			"  aw repo add PAY-142 infra:develop            new branch, from develop\n" +
			"  aw repo add PAY-142 ~/src/api@fix-1:release  new branch fix-1, from release\n\n" +
			"A branch that exists locally is used as it is; one that exists only on a\n" +
			"remote is checked out tracking it; otherwise it is created from the base,\n" +
			"which defaults to origin/HEAD. The repo name defaults to the registered\n" +
			"name or the directory's base name; --name overrides it.\n\n" +
			"Nothing is created on disk here — `aw start` makes the worktrees.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			t, err := a.loadTask(args[0])
			if err != nil {
				return err
			}
			c, err := a.Store.LoadConfig()
			if err != nil {
				return err
			}
			if err := a.addRepoSpec(cmd.Context(), t, c, args[1], opts); err != nil {
				return err
			}
			return a.Store.Save(t)
		},
	}
	cmd.Flags().StringVar(&opts.Name, "name", "", "repo name (default: the registered name or the directory's base name)")
	cmd.Flags().StringVar(&opts.Branch, "branch", "", "branch to use, the same as REPO@BRANCH (default: agent/TASK)")
	cmd.Flags().StringVar(&opts.Base, "base", "", "ref to branch from, the same as REPO:BASE (default: origin/HEAD, then the local default branch)")
	cmd.Flags().StringArrayVar(&opts.Agent, "agent", nil, "agent command to run in this worktree, repeat for arguments")
	cmd.Flags().StringVar(&opts.PostCreate, "post-create", "",
		"shell command run once in a new worktree, e.g. 'cp ../../.env . && pnpm install'")
	return cmd
}

// repoSpec is one repository as typed on the command line:
//
//	[NAME=]SOURCE[@BRANCH][:BASE]
//
// SOURCE is a registered name or a path. The spec splits at the first '@',
// which no repository name contains, and then at the first ':', which git
// forbids in ref names; a branch keeps any later '@' of its own.
type repoSpec struct {
	Name, Source, Branch, Base string
}

const repoSpecForm = "[NAME=]REPO[@BRANCH][:BASE]"

func parseRepoSpec(spec string) (repoSpec, error) {
	var s repoSpec
	rest := spec
	// '=' separates a name only when what precedes it could be one, since a
	// branch name may itself contain '='.
	if name, source, found := strings.Cut(spec, "="); found && !strings.ContainsAny(name, "@:/") {
		if name == "" || source == "" {
			return s, fmt.Errorf("invalid repository %q: expected %s", spec, repoSpecForm)
		}
		s.Name, rest = name, source
	}
	// A directory that exists as typed is taken literally, so a path that
	// happens to contain '@' or ':' still works.
	if looksLikePath(rest) && isDir(expandHome(rest)) {
		s.Source = rest
		return s, nil
	}

	source, branch, hasBranch := strings.Cut(rest, "@")
	tail := source
	if hasBranch {
		tail = branch
	}
	head, base, hasBase := strings.Cut(tail, ":")
	if hasBranch {
		s.Source, s.Branch = source, head
	} else {
		s.Source = head
	}
	s.Base = base
	if s.Source == "" || (hasBranch && s.Branch == "") || (hasBase && s.Base == "") {
		return s, fmt.Errorf("invalid repository %q: expected %s", spec, repoSpecForm)
	}
	return s, nil
}

func looksLikePath(s string) bool {
	return strings.ContainsRune(s, '/') || strings.HasPrefix(s, "~") || strings.HasPrefix(s, ".") || filepath.IsAbs(s)
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// addRepoSpec parses a REPO argument and adds it. A part given both in the
// spec and as a flag is refused rather than one silently winning.
func (a *App) addRepoSpec(ctx context.Context, t *task.Task, c *task.Config, spec string, opts addRepoOptions) error {
	s, err := parseRepoSpec(spec)
	if err != nil {
		return err
	}
	for _, p := range []struct{ flag, fromSpec, fromFlag string }{
		{"--name", s.Name, opts.Name}, {"--branch", s.Branch, opts.Branch}, {"--base", s.Base, opts.Base},
	} {
		if p.fromSpec != "" && p.fromFlag != "" {
			return fmt.Errorf("%s is given twice, in %q and as %s", strings.TrimPrefix(p.flag, "--"), spec, p.flag)
		}
	}
	opts.Name = firstNonEmpty(opts.Name, s.Name)
	opts.Branch = firstNonEmpty(opts.Branch, s.Branch)
	opts.Base = firstNonEmpty(opts.Base, s.Base)

	path := s.Source
	if !looksLikePath(s.Source) {
		if p, ok := c.Repos[s.Source]; ok {
			path = p
			opts.Name = firstNonEmpty(opts.Name, s.Source)
		} else if !isDir(s.Source) {
			return fmt.Errorf("no repository registered as %q%s", s.Source, registeredHint(c))
		}
	}
	return a.addRepo(ctx, t, c, path, opts)
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// addRepo validates the repository and records it on the task.
func (a *App) addRepo(ctx context.Context, t *task.Task, c *task.Config, path string, opts addRepoOptions) error {
	canonical, err := a.Git.Validate(ctx, expandHome(path))
	if err != nil {
		return err
	}

	name := opts.Name
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
		branch = c.Branch(t.Name)
	}
	if err := a.Git.CheckBranchName(ctx, branch); err != nil {
		return err
	}

	// Refs the user named but this clone has not fetched yet are the ones
	// most likely to be new, so fetch once before calling them missing.
	fetched := false
	var fetchErr error
	fetch := func() {
		if !fetched {
			fetched = true
			_, fetchErr = a.Git.FetchRemotes(ctx, canonical, opts.Base)
		}
	}

	// Resolve the base now so that `aw task show` can report it before anything
	// is created, and so a later start cannot silently pick a different one.
	base := opts.Base
	if base == "" {
		base, err = a.Git.ResolveBase(ctx, canonical)
	} else {
		base, err = a.Git.ResolveRef(ctx, canonical, opts.Base)
		if err == nil && base == "" {
			fetch()
			base, err = a.Git.ResolveRef(ctx, canonical, opts.Base)
		}
		if err == nil && base == "" {
			err = fmt.Errorf("base %q is not a ref in %s, locally or on a remote%s", opts.Base, canonical, fetchNote(fetchErr))
		}
	}
	if err != nil {
		return err
	}

	// Report what start will do with the branch, so a mistyped existing branch
	// shows up now as "new", not later as a worktree without the work in it.
	local, err := a.Git.BranchExists(ctx, canonical, branch)
	if err != nil {
		return err
	}
	remote := ""
	if !local {
		if remote, err = a.Git.RemoteBranch(ctx, canonical, branch); err == nil && remote == "" && opts.Branch != "" {
			fetch()
			remote, err = a.Git.RemoteBranch(ctx, canonical, branch)
		}
		if err != nil {
			return err
		}
	}
	var branchNote string
	switch {
	case local:
		branchNote = "existing branch"
	case remote != "":
		branchNote = "will track " + remote
	default:
		branchNote = "new, from " + base
		if opts.Branch != "" {
			a.warnf("note: %s has no branch %s locally or on a remote%s; start will create it from %s\n",
				name, branch, fetchNote(fetchErr), base)
		}
	}
	if local {
		if wt, err := a.Git.FindWorktreeForBranch(ctx, canonical, branch); err == nil && wt != nil {
			a.warnf("note: %s is checked out at %s; start will refuse until it is switched away there\n", branch, wt.Path)
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
	a.printf("  branch:   %s (%s)\n", branch, branchNote)
	if local || remote != "" {
		a.printf("  base:     %s\n", base)
	}
	a.printf("  worktree: %s\n", a.Store.WorktreePath(t.Name, name))
	return nil
}

// fetchNote says a lookup used stale refs because fetching failed.
func fetchNote(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf(" (fetch failed: %v)", err)
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
