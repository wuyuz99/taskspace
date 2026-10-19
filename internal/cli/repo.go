package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"github.com/wuyuz99/taskspace/internal/task"
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
	var resume resumeOptions
	cmd := &cobra.Command{
		Use:   "update TASK REPO",
		Short: "Update saved agent and setup settings",
		Long: "update replaces the repository's saved agent command or post-create hook.\n" +
			"Running windows are untouched: agent settings apply when a window is\n" +
			"created or explicitly restarted, and hooks run only when a worktree is\n" +
			"newly created.\n\n" +
			"  tspace repo update PAY-142 backend --agent codex --agent=--resume\n" +
			"  tspace repo update PAY-142 backend --shell --post-create=''",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			setAgent, setHook := cmd.Flags().Changed("agent"), cmd.Flags().Changed("post-create")
			if setAgent && shell {
				return fmt.Errorf("choose either --agent or --shell")
			}
			if !setAgent && !setHook && !shell && !resume.changed(cmd) {
				return fmt.Errorf("specify --agent, --shell, --resume, --no-resume or --post-create")
			}
			if err := resume.validate(cmd); err != nil {
				return err
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
			// The agent repository's window runs the task's agent, so that is
			// what its agent flags change.
			taskAgent := args[1] == t.AgentRepo
			before := t.RepoAgent(args[1])
			switch {
			case taskAgent && setAgent:
				t.Agent = agent
			case taskAgent && shell:
				t.Agent = nil
			case setAgent:
				r.Agent = agent
			case shell:
				r.Agent = nil
			}
			var resumeCleared bool
			after := r.Agent
			if taskAgent {
				after = t.Agent
			}
			agentChanged := (setAgent || shell) && !slices.Equal(before, after)
			if taskAgent {
				t.Resume, resumeCleared = resume.apply(cmd, t.Resume, agentChanged)
			} else {
				r.Resume, resumeCleared = resume.apply(cmd, r.Resume, agentChanged)
			}
			if setHook {
				r.PostCreate = hook
			}
			t.SetRepo(args[1], r)
			if len(t.ResumeFor(args[1])) > 0 && len(t.RepoAgent(args[1])) == 0 {
				return fmt.Errorf("--resume resumes the window's agent, and %s runs a shell; give one with --agent", args[1])
			}
			if err := a.Store.Save(t); err != nil {
				return err
			}
			a.printf("updated %s/%s\n", t.Name, args[1])
			if resumeCleared {
				a.warnf("note: the resume command went with the old agent; set one for this agent with --resume\n")
			}
			if resume.changed(cmd) {
				a.printf("  resume: %s\n", resumeDisplay(t.ResumeFor(args[1])))
			}
			if setAgent || shell {
				which := ""
				if taskAgent {
					which = "; the task's agent, which runs in this window"
				}
				a.printf("  agent: %s (applies on window creation or explicit restart%s)\n", agentDisplay(task.Repo{Agent: t.RepoAgent(args[1])}), which)
			}
			if setHook {
				a.printf("  post_create: %s (applies to newly created worktrees)\n", dash(hook))
			}
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&agent, "agent", nil, "replace the agent command, one flag per word")
	cmd.Flags().BoolVar(&shell, "shell", false, "clear the agent command and use the default shell")
	resume.flags(cmd, true)
	cmd.Flags().StringVar(&hook, "post-create", "", "replace the setup hook; an empty value clears it")
	return cmd
}

type addRepoOptions struct {
	Name       string
	Branch     string
	Base       string
	Agent      []string
	Resume     []string
	PostCreate string
}

func (a *App) newRepoAddCmd() *cobra.Command {
	var opts addRepoOptions
	var resume resumeOptions

	cmd := &cobra.Command{
		Use:   "add TASK REPO",
		Short: "Attach a repository to a task",
		Long: "add registers a repository with a task. REPO is a name registered with\n" +
			"`tspace config repo` or a path, optionally followed by a branch and a base:\n\n" +
			"  tspace repo add PAY-142 backend                  new branch agent/PAY-142\n" +
			"  tspace repo add PAY-142 frontend@feature/x       an existing branch\n" +
			"  tspace repo add PAY-142 infra:develop            new branch, from develop\n" +
			"  tspace repo add PAY-142 ~/src/api@fix-1:release  new branch fix-1, from release\n\n" +
			"A branch that exists locally is used as it is; one that exists only on a\n" +
			"remote is checked out tracking it; otherwise it is created from the base,\n" +
			"which defaults to origin/HEAD. The repo name defaults to the registered\n" +
			"name or the directory's base name; --name overrides it.\n\n" +
			"The repository's window runs a shell unless --agent names a command, one\n" +
			"flag per word:\n\n" +
			"  tspace repo add PAY-142 backend --agent claude --agent=--model --agent opus\n\n" +
			"--post-create is run once by `sh -c` in the new worktree, which has no\n" +
			".env, node_modules or venv of its own. It is given TSPACE_TASK, TSPACE_REPO,\n" +
			"TSPACE_REPO_PATH (the source repository), TSPACE_WORKTREE, TSPACE_TASK_DIR,\n" +
			"TSPACE_CONTEXT_DIR and TSPACE_ROOT. A failing hook is reported and the\n" +
			"worktree is kept.\n\n" +
			"The first repository of a task with none, and no session, runs the task's\n" +
			"agent in its own window, as `tspace task create` does for one. Adding a\n" +
			"second to such a task leaves that agent where it is and gives the task a\n" +
			"root window with a shell, opened at once if the session is running.\n\n" +
			"Nothing else is created on disk here — `tspace start` makes the worktrees.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			t, err := a.loadTask(args[0])
			if err != nil {
				return err
			}
			c, err := a.Store.LoadConfig()
			if err != nil {
				return err
			}
			if err := resume.validate(cmd); err != nil {
				return err
			}
			opts.Resume = resume.argv
			if len(opts.Resume) > 0 && len(opts.Agent) == 0 {
				return fmt.Errorf("--resume resumes the repository's agent; give one with --agent")
			}
			first := len(t.Repos) == 0
			if err := a.addRepoSpec(ctx, t, c, args[1], opts); err != nil {
				return err
			}
			running := a.sessionRunning(ctx, t)
			openRoot := false
			switch {
			case first && t.HasRootWindow() && !t.RootWindow && !running && t.CanRunAgentIn(t.RepoNames()[0]):
				// Nothing has run in a root window that the agent could be
				// moved out of, so this task starts as a single-repo one.
				t.AgentRepo = t.RepoNames()[0]
				a.printf("  agent:    %s (runs in this worktree)\n", agentDisplay(task.Repo{Agent: t.RepoAgent(t.AgentRepo)}))
			case !t.HasRootWindow() && len(t.Repos) > 1:
				t.RootWindow = true
				openRoot = running
				if !running {
					a.printf("the next start opens a root window for %s; its agent stays in %s\n", t.Name, t.AgentRepo)
				}
			}
			if err := a.Store.Save(t); err != nil {
				return err
			}
			if openRoot {
				a.openRootWindow(ctx, t)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&opts.Name, "name", "", "repo name (default: the registered name or the directory's base name)")
	cmd.Flags().StringVar(&opts.Branch, "branch", "", "branch to use, the same as REPO@BRANCH (default: agent/TASK)")
	cmd.Flags().StringVar(&opts.Base, "base", "", "ref to branch from, the same as REPO:BASE (default: origin/HEAD, then the local default branch)")
	cmd.Flags().StringArrayVar(&opts.Agent, "agent", nil, "agent command to run in this worktree, one flag per word")
	resume.flags(cmd, false)
	cmd.Flags().StringVar(&opts.PostCreate, "post-create", "",
		`shell command run once in a new worktree, e.g. 'cp "$TSPACE_REPO_PATH/.env" . && pnpm install'`)
	return cmd
}

// sessionRunning reports whether the task's session is up. A runtime that
// cannot be asked has nothing running that tspace could act on.
func (a *App) sessionRunning(ctx context.Context, t *task.Task) bool {
	if a.Runtime == nil || a.Runtime.Available(ctx) != nil {
		return false
	}
	running, err := a.Runtime.HasSession(ctx, a.Runtime.SessionName(t.Name))
	return err == nil && running
}

// openRootWindow adds the task-root window to a running session, for a task
// that just gained one. Failing is only a warning: the record says the
// window exists, so the next start creates it.
func (a *App) openRootWindow(ctx context.Context, t *task.Task) {
	w := a.rootWindow(t)
	err := os.MkdirAll(w.Dir, 0o755)
	if err == nil {
		err = a.Runtime.OpenWindow(ctx, a.Runtime.SessionName(t.Name), w)
	}
	if err != nil {
		a.warnf("could not open the root window now (%v); the next tspace start %s will\n", err, t.Name)
		return
	}
	a.printf("opened the root window for %s (a shell at %s); its agent stays in %s\n", t.Name, w.Dir, t.AgentRepo)
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

	// Resolve the base now so that `tspace task show` can report it before anything
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
	worktree := a.Store.WorktreePath(t.Name, name)
	if local {
		// Checked out at this repo's own worktree path, as after repo rm and
		// add again, start reuses the worktree; only elsewhere does it refuse.
		if wt, err := a.Git.FindWorktreeForBranch(ctx, canonical, branch); err == nil && wt != nil {
			if own, err := a.Git.FindWorktree(ctx, canonical, worktree); err == nil && own != nil && own.Branch == branch {
				a.printf("note: %s is already checked out at %s; start will reuse that worktree\n", branch, own.Path)
			} else {
				a.warnf("note: %s is checked out at %s; start will refuse until it is switched away there\n", branch, wt.Path)
			}
		}
	}

	t.SetRepo(name, task.Repo{
		Path:       canonical,
		Branch:     branch,
		Base:       base,
		Worktree:   worktree,
		Agent:      opts.Agent,
		Resume:     opts.Resume,
		PostCreate: opts.PostCreate,
	})

	a.printf("added %s to %s\n", name, t.Name)
	a.printf("  repo:     %s\n", canonical)
	a.printf("  branch:   %s (%s)\n", branch, branchNote)
	if local || remote != "" {
		a.printf("  base:     %s\n", base)
	}
	a.printf("  worktree: %s\n", worktree)
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
			"`tspace clean TASK` for that, so the safety checks apply.",
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
			if args[1] == t.AgentRepo && a.sessionRunning(cmd.Context(), t) {
				return fmt.Errorf("the task's agent runs in %s's window; stop the task first: tspace stop %s", args[1], t.Name)
			}
			if args[1] == t.AgentRepo && len(t.Resume) > 0 {
				a.warnf("note: the task's agent moves to the root window, at %s; a resume command that finds its conversation by directory will not find the one from %s\n", a.Store.TaskDir(t.Name), args[1])
			}
			t.RemoveRepo(args[1])
			if err := a.Store.Save(t); err != nil {
				return err
			}
			a.printf("removed %s from %s\n", args[1], t.Name)
			// After clean there is no worktree left to speak of.
			if _, err := os.Stat(r.Worktree); err == nil {
				a.warnf("note: the worktree at %s was left in place\n", r.Worktree)
			}
			return nil
		},
	}
}
