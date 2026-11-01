package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/wuyuz99/taskspace/internal/task"
	"github.com/wuyuz99/taskspace/internal/workspace"
)

func (a *App) newRepoCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "repo",
		Short: "Attach, configure, and detach repositories",
	}
	cmd.AddCommand(a.newRepoAddCmd(), a.newRepoRmCmd(), a.newRepoUpdateCmd(), a.newRepoMoveCmd())
	return cmd
}

func (a *App) newRepoUpdateCmd() *cobra.Command {
	var agent []string
	var hook, base, branch string
	var shell, rebase bool
	var resume resumeOptions
	cmd := &cobra.Command{
		Use:   "update TASK REPO",
		Short: "Update a repository's agent, setup, branch or base",
		Long: "update replaces the repository's saved agent command or post-create hook.\n" +
			"Running windows are untouched: agent settings apply when a window is\n" +
			"created or explicitly restarted, and hooks run only when a worktree is\n" +
			"newly created.\n\n" +
			"  tspace repo update PAY-142 backend --agent codex --agent=--resume\n" +
			"  tspace repo update PAY-142 backend --shell --post-create=''\n\n" +
			"In the repository whose window runs the task's agent, --agent replaces\n" +
			"the task's agent; --shell refuses, since the task would have no agent\n" +
			"left (tspace task update TASK --shell drops it on purpose).\n\n" +
			"--branch renames the task's branch, checked out in the worktree if there\n" +
			"is one; it never takes over a branch that already exists. --base changes\n" +
			"what the branch is compared with, for status and clean, and what start\n" +
			"creates it from; the branch itself stays where it is unless --rebase,\n" +
			"which moves its commits from the old base onto the new one. A rebase that\n" +
			"stops on a conflict is undone, leaving everything as it was:\n\n" +
			"  tspace repo update PAY-142 backend --branch me/PAY-142\n" +
			"  tspace repo update PAY-142 backend --base origin/release --rebase",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			setAgent, setHook := cmd.Flags().Changed("agent"), cmd.Flags().Changed("post-create")
			setBase, setBranch := cmd.Flags().Changed("base"), cmd.Flags().Changed("branch")
			if setAgent && shell {
				return fmt.Errorf("choose either --agent or --shell")
			}
			if !setAgent && !setHook && !shell && !resume.changed(cmd) && !setBase && !setBranch {
				return fmt.Errorf("specify --agent, --shell, --resume, --no-resume, --post-create, --branch or --base")
			}
			if rebase && !setBase {
				return fmt.Errorf("--rebase moves the branch onto a new base; give it with --base")
			}
			if (setBase && strings.TrimSpace(base) == "") || (setBranch && strings.TrimSpace(branch) == "") {
				return fmt.Errorf("--base and --branch need a value")
			}
			if err := resume.validate(cmd); err != nil {
				return err
			}
			if setAgent && (len(agent) == 0 || strings.TrimSpace(agent[0]) == "") {
				return fmt.Errorf("agent command is empty; use --shell to restore the default shell")
			}
			name, err := a.resolveTask(args[0])
			if err != nil {
				return err
			}
			res, err := a.workspaceFor(cmd).UpdateRepo(cmd.Context(), name, args[1], workspace.UpdateRepoOptions{
				SetAgent: setAgent, Agent: agent, Shell: shell,
				SetResume: cmd.Flags().Changed("resume"), Resume: resume.argv, NoResume: resume.none,
				SetHook: setHook, PostCreate: hook,
				Branch: branch, Base: base, Rebase: rebase,
			})
			if err != nil {
				return err
			}
			t, taskAgent := res.Task, res.TaskAgent
			a.printf("updated %s/%s\n", t.Name, args[1])
			for _, n := range res.Notes {
				a.printf("  %s\n", n)
			}
			if res.ResumeCleared {
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
	cmd.Flags().StringVar(&branch, "branch", "", "rename the task's branch")
	cmd.Flags().StringVar(&base, "base", "", "compare the branch with, and create it from, this ref instead")
	cmd.Flags().BoolVar(&rebase, "rebase", false, "with --base, move the branch's commits from the old base onto the new one")
	return cmd
}

type addRepoOptions = workspace.AddRepoOptions

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
			"root window with a shell, opened at once if the session is running.\n" +
			"If tspace cannot tell whether the session is running, the agent stays in\n" +
			"the root window, and a new root window waits for the next start.\n\n" +
			"Nothing else is created on disk here — `tspace start` makes the worktrees.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			t, unlock, err := a.lockTask(cmd, args[0], true)
			if err != nil {
				return err
			}
			defer unlock()
			ctx := cmd.Context()
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
			ws := a.workspace()
			openRoot := ws.SettleLayout(ctx, t, first)
			if err := a.Store.Save(t); err != nil {
				return err
			}
			if openRoot {
				ws.OpenRootWindow(ctx, t)
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

// samePath reports whether two paths name the same place, as git and tspace
// spell them.
func samePath(x, y string) bool { return workspace.SamePath(x, y) }

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
	return a.workspace().AddRepo(ctx, t, c, expandHome(path), opts)
}

// removedRepoText is repo rm's last line, which dash shows too.
func removedRepoText(repo, taskName string) string {
	return fmt.Sprintf("removed %s from %s", repo, taskName)
}

// movedRepoText is repo move's first line, which dash shows too. name is what
// the repository is called in to, if not what it was.
func movedRepoText(repo, old, to, name string) string {
	text := fmt.Sprintf("moved %s from %s to %s", repo, old, to)
	if name != "" && name != repo {
		text += " as " + name
	}
	return text
}

func (a *App) newRepoMoveCmd() *cobra.Command {
	var to, name string
	cmd := &cobra.Command{
		Use:   "move OLD REPO --to NEW [--name NAME]",
		Short: "Move a repository, and its worktree, to another task",
		Long: "move takes a repository from one task to another: its worktree goes to the\n" +
			"new task's directory, with `git worktree move`, so uncommitted, untracked\n" +
			"and ignored files go with it. Its branch, base, agent, resume command and\n" +
			"setup stay as they are, and so do the saved windows that open in it.\n" +
			"--name calls it something else in the new task; `tspace repo update\n" +
			"--branch` renames the branch.\n\n" +
			"  tspace repo move PAY-142 backend --to PAY-150\n\n" +
			"Both tasks may be running, but it refuses while the repository's window or\n" +
			"one of its saved windows is open: close those with `tspace stop OLD REPO`.\n" +
			"It also refuses while OLD's session has a window with no saved repository,\n" +
			"which may be working in the worktree, and while OLD runs at all if this\n" +
			"repository's window runs OLD's agent, which moves to the root window. Run it\n" +
			"from outside the worktree. If anything fails, the worktree moves back.\n\n" +
			"An agent that resumes by directory leaves its conversation at the old path,\n" +
			"and a note names it, unless `tspace config carry-conversations` moves it\n" +
			"along; left behind, the agent starts fresh. The task's own agent stays with\n" +
			"its task. Nothing is started: `tspace start NEW REPO` opens the window.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if to == "" {
				return fmt.Errorf("name the task to move it to with --to")
			}
			for _, n := range []string{args[0], to} {
				if err := explicitTask(n); err != nil {
					return err
				}
			}
			if err := a.workspaceFor(cmd).MoveRepo(cmd.Context(), args[0], args[1], to, name); err != nil {
				return err
			}
			a.printf("%s\n", movedRepoText(args[1], args[0], to, name))
			a.printf("start its window with: tspace start %s %s\n", to, firstNonEmpty(name, args[1]))
			return nil
		},
	}
	cmd.Flags().StringVar(&to, "to", "", "the task to move it to")
	cmd.Flags().StringVar(&name, "name", "", "repo name in the new task (default: its name now)")
	return cmd
}

func (a *App) newRepoRmCmd() *cobra.Command {
	var keep, force bool
	cmd := &cobra.Command{
		Use:   "rm TASK REPO",
		Short: "Detach a repository from a task",
		Long: "rm safely cleans the repository's worktree and closes its windows before\n" +
			"forgetting it. --keep-worktree only forgets it, leaving its files and windows\n" +
			"under your control; clean can no longer find them afterward. --force discards\n" +
			"the work clean would refuse to lose.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := explicitTask(args[0]); err != nil {
				return err
			}
			left, err := a.workspaceFor(cmd).RemoveRepo(cmd.Context(), args[0], args[1], keep, force)
			if err != nil {
				return err
			}
			a.printf("%s\n", removedRepoText(args[1], args[0]))
			if left != "" {
				a.warnf("note: the worktree at %s was left in place\n", left)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&keep, "keep-worktree", false, "forget the repository without removing its worktree or windows")
	cmd.Flags().BoolVar(&force, "force", false, "discard uncommitted work and unmerged commits in the worktree")
	cmd.MarkFlagsMutuallyExclusive("keep-worktree", "force")
	return cmd
}
