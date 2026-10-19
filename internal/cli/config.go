package cli

import (
	"fmt"
	neturl "net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/wuyuz99/taskspace/internal/task"
)

func (a *App) newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Show and set user-wide defaults",
		Long: "Defaults live in $TSPACE_ROOT/config.json. They are applied when a task is\n" +
			"created and recorded on it, so changing one never changes an existing task.",
		Args: noSubcommandArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.Store.LoadConfig()
			if err != nil {
				return err
			}
			a.printf("config: %s\n", a.Store.ConfigPath())
			a.printf("agent:  %s\n", defaultAgentDisplay(c.Agent))
			if len(c.Resume) > 0 {
				a.printf("resume: %s\n", shellJoin(c.Resume))
			}
			a.printf("branch: %s (new repositories' default branch)\n", c.Branch("TASK"))
			a.printf("multiplexer: %s\n", multiplexerDisplay(multiplexerName(c), c))
			if len(c.Terminal) > 0 {
				a.printf("terminal: %s\n", shellJoin(c.Terminal))
			}
			if c.TicketPattern != "" {
				a.printf("ticket pattern: %s%s\n", c.TicketPattern, ticketPatternProblem(c))
			}
			if len(c.Repos) == 0 {
				a.printf("repos:  none registered (tspace config repo NAME PATH)\n")
				return nil
			}
			a.printf("repos:\n")
			a.printRepoCatalog(c)
			return nil
		},
	}
	cmd.AddCommand(a.newConfigAgentCmd(), a.newConfigRepoCmd(), a.newConfigBranchPrefixCmd(), a.newConfigMultiplexerCmd(), a.newConfigHooksCmd(), a.newConfigResumeCmd(), a.newConfigTicketPatternCmd(), a.newConfigTerminalCmd())
	return cmd
}

func (a *App) newConfigRepoCmd() *cobra.Command {
	var unset bool
	var into string
	cmd := &cobra.Command{
		Use:   "repo [NAME [PATH | URL] | NAME --unset]",
		Short: "Register source repositories by name",
		Long: "Register each repository you work in once, then give tasks its name\n" +
			"instead of a path:\n\n" +
			"  tspace config repo backend ~/src/backend\n" +
			"  tspace task create PAY-142 backend frontend@feature/x infra:develop\n\n" +
			"There are no add, list or rm subcommands; the arguments say which:\n\n" +
			"  tspace config repo                  list the registered repositories\n" +
			"  tspace config repo backend          print backend's path\n" +
			"  tspace config repo backend PATH     register backend, or move it to PATH\n" +
			"  tspace config repo backend URL      clone it, then register the clone\n" +
			"  tspace config repo backend --unset  forget backend\n\n" +
			"A URL (https://, ssh://, file://, or git@host:path) is cloned into\n" +
			"$TSPACE_ROOT/repos/NAME, or --into DIR, with git's own credentials and\n" +
			"progress. A clone of the same URL already there is registered as it is.\n\n" +
			"Tasks record the path when a repository is added, so moving or\n" +
			"unregistering one later leaves existing tasks alone.",
		Args: cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if into != "" && (unset || len(args) != 2) {
				return fmt.Errorf("--into says where to clone a URL given as PATH")
			}
			c, err := a.Store.LoadConfig()
			if err != nil {
				return err
			}
			switch {
			case unset && len(args) != 1:
				return fmt.Errorf("--unset takes exactly one repository name")
			case len(args) == 0:
				if len(c.Repos) == 0 {
					a.printf("no repositories registered; add one with: tspace config repo NAME PATH\n")
					return nil
				}
				a.printRepoCatalog(c)
				return nil
			case len(args) == 1 && !unset:
				p, ok := c.Repos[args[0]]
				if !ok {
					if hint, verb := configRepoVerbs[args[0]]; verb {
						return fmt.Errorf("tspace config repo has no %q subcommand; %s", args[0], hint)
					}
					return fmt.Errorf("no repository registered as %q%s", args[0], registeredHint(c))
				}
				a.printf("%s\n", p)
				return nil
			}

			name := args[0]
			// `tspace config repo add ~/src/api` reads as a subcommand, and would
			// otherwise quietly register a repository called "add".
			if hint, verb := configRepoVerbs[name]; verb && !unset {
				return fmt.Errorf("%q cannot be a repository name, because it reads as a subcommand; %s", name, hint)
			}
			if unset {
				if _, ok := c.Repos[name]; !ok {
					return fmt.Errorf("no repository registered as %q%s", name, registeredHint(c))
				}
				delete(c.Repos, name)
				if err := a.Store.SaveConfig(c); err != nil {
					return err
				}
				a.printf("unregistered %s; tasks that already use it are unchanged\n", name)
				return nil
			}
			if err := task.ValidateName("repo", name); err != nil {
				return err
			}
			path := expandHome(args[1])
			// A directory by that name wins over reading it as a URL.
			clone := looksLikeURL(args[1]) && !isDir(path)
			if into != "" && !clone {
				return fmt.Errorf("--into says where to clone a URL; %s is a path", args[1])
			}
			if clone {
				dest := filepath.Join(a.Store.ReposDir(), name)
				if into != "" {
					dest = expandHome(into)
				}
				if dest, err = filepath.Abs(dest); err != nil {
					return err
				}
				// Refused before cloning, so no clone is left behind.
				for _, other := range c.RepoNames() {
					if other != name && samePath(c.Repos[other], dest) {
						return fmt.Errorf("%s is already registered as %q", dest, other)
					}
				}
				if path, err = a.cloneRepo(cmd, args[1], dest); err != nil {
					return err
				}
			}
			canonical, err := a.Git.Validate(cmd.Context(), path)
			if err != nil {
				return err
			}
			for _, other := range c.RepoNames() {
				if other != name && c.Repos[other] == canonical {
					return fmt.Errorf("%s is already registered as %q", canonical, other)
				}
			}
			if c.Repos == nil {
				c.Repos = map[string]string{}
			}
			c.Repos[name] = canonical
			if err := a.Store.SaveConfig(c); err != nil {
				return err
			}
			a.printf("registered %s: %s\n", name, canonical)
			return nil
		},
	}
	cmd.Flags().BoolVar(&unset, "unset", false, "forget a registered repository")
	cmd.Flags().StringVar(&into, "into", "", "clone a URL into this directory instead of $TSPACE_ROOT/repos/NAME")
	return cmd
}

// cloneRepo clones url into dest, an absolute path, for config repo, and
// returns the clone's path. A complete clone of url already at dest is used
// as it is, and an empty directory is cloned into; anything else there is
// refused rather than cloned over or registered in its place. A clone that
// fails or is interrupted is removed, if config repo made its directory.
func (a *App) cloneRepo(cmd *cobra.Command, url, dest string) (string, error) {
	ctx := cmd.Context()
	shown := redactURL(url)
	made := true
	if entries, err := os.ReadDir(dest); err == nil {
		have, err := a.Git.OriginURL(ctx, dest)
		switch {
		case err == nil && have == url:
			// Finished, too: an interrupted clone has its remote but no commit.
			if _, err := a.Git.Commit(ctx, dest, "HEAD"); err != nil {
				return "", fmt.Errorf("%s holds an unfinished clone of %s; remove it and run this again", dest, shown)
			}
			a.printf("%s is already a clone of %s\n", dest, shown)
			return dest, nil
		case len(entries) > 0:
			return "", fmt.Errorf("%s already exists and is not a clone of %s; choose another place with --into", dest, shown)
		}
		made = false
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("%s exists and cannot be cloned into: %v; choose another place with --into", dest, err)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", err
	}
	a.printf("cloning %s into %s\n", shown, dest)
	if err := a.Git.Clone(ctx, url, dest, cmd.InOrStdin(), a.Err); err != nil {
		if made {
			_ = os.RemoveAll(dest)
		}
		return "", fmt.Errorf("git clone %s: %w", shown, err)
	}
	return dest, nil
}

// redactURL hides a password or token written into a URL, which would
// otherwise end up in scrollback and logs.
func redactURL(s string) string {
	u, err := neturl.Parse(s)
	if err != nil || u.User == nil {
		return s
	}
	u.User = neturl.User("***")
	return u.String()
}

// looksLikeURL reports whether a repository argument is something to clone:
// a URL with a scheme, or git's scp-like [user@]host:path where the host has
// a user or a dot, as in git@github.com:o/r. A Windows drive (C:\src) is not
// one, nor a mistyped path that happens to hold a colon.
func looksLikeURL(s string) bool {
	if strings.Contains(s, "://") {
		return true
	}
	i := strings.Index(s, ":")
	if i <= 1 || strings.ContainsAny(s[:i], `/\`) {
		return false
	}
	return strings.Contains(s[:i], "@") || strings.Contains(s[:i], ".")
}

func (a *App) newConfigBranchPrefixCmd() *cobra.Command {
	var unset bool
	cmd := &cobra.Command{
		Use:   "branch-prefix [PREFIX | --unset]",
		Short: "Show or set the prefix of new task branches",
		Long: "A repository added to a task without a branch of its own gets\n" +
			"PREFIX + TASK, by default agent/TASK:\n\n" +
			"  tspace config branch-prefix me/        # me/PAY-142\n" +
			"  tspace config branch-prefix ''         # PAY-142\n\n" +
			"Branches are recorded when a repository is added, so changing the prefix\n" +
			"leaves existing tasks alone.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if unset && len(args) > 0 {
				return fmt.Errorf("choose either a prefix or --unset")
			}
			c, err := a.Store.LoadConfig()
			if err != nil {
				return err
			}
			switch {
			case unset:
				c.BranchPrefix = nil
			case len(args) == 0:
				a.printf("%s (e.g. %s)\n", strconv.Quote(c.Branch("")), c.Branch("PAY-142"))
				return nil
			default:
				prefix := args[0]
				if err := a.Git.CheckBranchName(cmd.Context(), prefix+"PAY-142"); err != nil {
					return fmt.Errorf("prefix %q does not make valid branch names: %w", prefix, err)
				}
				c.BranchPrefix = &prefix
			}
			if err := a.Store.SaveConfig(c); err != nil {
				return err
			}
			a.printf("new task branches: %s (applies to repositories added from now on)\n", c.Branch("TASK"))
			return nil
		},
	}
	cmd.Flags().BoolVar(&unset, "unset", false, "restore the default prefix, agent/")
	return cmd
}

func (a *App) newConfigTicketPatternCmd() *cobra.Command {
	var unset bool
	cmd := &cobra.Command{
		Use:   "ticket-pattern [REGEXP | --unset]",
		Short: "Show or set how a task's ticket is found in its name",
		Long: "A task created without --ticket gets as its ticket what REGEXP matches in\n" +
			"its name: the group named ticket, (?P<ticket>...), if there is one, else the\n" +
			"whole match. A name it does not match gets no ticket. Unset, names are never\n" +
			"read as tickets. The syntax is Go's (RE2):\n\n" +
			"  tspace config ticket-pattern '[A-Z][A-Z0-9]+-[0-9]+'      # ENG-123-login -> ENG-123\n" +
			"  tspace config ticket-pattern '^gh-(?P<ticket>[0-9]+)$'    # gh-42 -> 42\n\n" +
			"The ticket is recorded when the task is created, so changing the pattern\n" +
			"leaves existing tasks alone.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if unset && len(args) > 0 {
				return fmt.Errorf("choose either a pattern or --unset")
			}
			c, err := a.Store.LoadConfig()
			if err != nil {
				return err
			}
			switch {
			case unset:
				c.TicketPattern = ""
			case len(args) == 0:
				if c.TicketPattern == "" {
					a.printf("none; task names are not read as tickets\n")
				} else {
					a.printf("%s%s\n", c.TicketPattern, ticketPatternProblem(c))
				}
				return nil
			case args[0] == "":
				return fmt.Errorf("pattern is empty; use --unset to clear it")
			default:
				if _, err := task.CompileTicketPattern(args[0]); err != nil {
					return err
				}
				c.TicketPattern = args[0]
			}
			if err := a.Store.SaveConfig(c); err != nil {
				return err
			}
			if unset {
				a.printf("cleared the ticket pattern\n")
				return nil
			}
			a.printf("ticket pattern: %s (applies to tasks created from now on)\n", c.TicketPattern)
			return nil
		},
	}
	cmd.Flags().BoolVar(&unset, "unset", false, "stop reading tickets from task names")
	return cmd
}

func (a *App) newConfigTerminalCmd() *cobra.Command {
	var unset bool
	cmd := &cobra.Command{
		Use:   "terminal [COMMAND [ARGS...] | --unset]",
		Short: "Show or set how a task opens in a new terminal tab",
		Long: "dash's t key opens the selected task in a new tab or window of your\n" +
			"terminal, running `tspace open TASK` there. Every terminal does that its own\n" +
			"way and tspace knows none of them, so you tell it: the command is run with\n" +
			"tspace's own command added at the end, or, if an argument holds {command},\n" +
			"with the command as one shell string put there instead:\n\n" +
			"  tspace config terminal wt.exe -w 0 new-tab --        # Windows Terminal\n" +
			"  tspace config terminal wezterm cli spawn --\n" +
			"  tspace config terminal kitty @ launch --type=tab\n" +
			"  tspace config terminal gnome-terminal --tab --\n" +
			"  tspace config terminal osascript -e 'tell app \"Terminal\" to do script \"{command}\"'",
		RunE: func(cmd *cobra.Command, args []string) error {
			if unset && len(args) > 0 {
				return fmt.Errorf("choose either a command or --unset")
			}
			c, err := a.Store.LoadConfig()
			if err != nil {
				return err
			}
			switch {
			case unset:
				c.Terminal = nil
			case len(args) == 0:
				if len(c.Terminal) == 0 {
					a.printf("none; dash cannot open new tabs until one is set\n")
				} else {
					a.printf("%s\n", shellJoin(c.Terminal))
				}
				return nil
			case strings.TrimSpace(args[0]) == "":
				return fmt.Errorf("terminal command is empty; use --unset to clear it")
			default:
				c.Terminal = args
			}
			if err := a.Store.SaveConfig(c); err != nil {
				return err
			}
			if unset {
				a.printf("cleared the terminal command\n")
				return nil
			}
			a.printf("terminal: %s\n", shellJoin(c.Terminal))
			return nil
		},
	}
	// Flags after COMMAND belong to it, not to tspace.
	cmd.Flags().SetInterspersed(false)
	cmd.Flags().BoolVar(&unset, "unset", false, "clear the terminal command")
	return cmd
}

// ticketPatternProblem notes why a ticket pattern does nothing, as one edited
// into config.json by hand may.
func ticketPatternProblem(c *task.Config) string {
	if _, err := task.CompileTicketPattern(c.TicketPattern); err != nil {
		return " (not used: " + err.Error() + ")"
	}
	return ""
}

// configRepoVerbs are the subcommands people expect `tspace config repo` to have,
// each with what to type instead.
var configRepoVerbs = map[string]string{
	"add":    "register one with: tspace config repo NAME PATH",
	"set":    "register one with: tspace config repo NAME PATH",
	"list":   "list them with: tspace config repo",
	"ls":     "list them with: tspace config repo",
	"rm":     "forget one with: tspace config repo NAME --unset",
	"remove": "forget one with: tspace config repo NAME --unset",
}

func (a *App) printRepoCatalog(c *task.Config) {
	tw := tabwriter.NewWriter(a.Out, 0, 0, 2, ' ', 0)
	for _, name := range c.RepoNames() {
		fmt.Fprintf(tw, "  %s\t%s\n", name, c.Repos[name])
	}
	tw.Flush()
}

func registeredHint(c *task.Config) string {
	if len(c.Repos) == 0 {
		return "; register it with: tspace config repo NAME PATH"
	}
	return " (registered: " + joinNames(c.RepoNames()) + ")"
}

func (a *App) newConfigAgentCmd() *cobra.Command {
	var unset bool
	cmd := &cobra.Command{
		Use:   "agent [COMMAND [ARGS...] | --unset]",
		Short: "Show or set the default task-root agent",
		Long: "With a command, new tasks run it in their root window, unless task create\n" +
			"is given --agent or --shell, or its --project recipe names an agent.\n" +
			"Everything after COMMAND is passed to it, flags included:\n\n" +
			"  tspace config agent claude --model opus\n\n" +
			"{context_dir} and {task_dir} in the command are replaced, when a window\n" +
			"starts, by the task's .context directory and its directory, so an agent can\n" +
			"be pointed at the task's notes with its own flag:\n\n" +
			"  tspace config agent claude --add-dir {context_dir}\n\n" +
			"Existing tasks keep the agent they were created with; change one with\n" +
			"tspace task update TASK --agent CMD.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if unset && len(args) > 0 {
				return fmt.Errorf("choose either a command or --unset")
			}
			c, err := a.Store.LoadConfig()
			if err != nil {
				return err
			}
			before := c.Agent
			switch {
			case unset:
				c.Agent = nil
			case len(args) == 0:
				a.printf("%s\n", defaultAgentDisplay(c.Agent))
				return nil
			case strings.TrimSpace(args[0]) == "":
				return fmt.Errorf("agent command is empty; use --unset to clear the default")
			default:
				c.Agent = args
			}
			// The default resume command resumes the default agent only.
			if len(c.Resume) > 0 && !slices.Equal(before, c.Agent) {
				c.Resume = nil
				a.warnf("note: the default resume command went with the old agent; set one for this agent with tspace config resume\n")
			}
			if err := a.Store.SaveConfig(c); err != nil {
				return err
			}
			if unset {
				a.printf("cleared the default agent; new tasks' root windows run a shell\n")
				return nil
			}
			a.printf("default agent: %s (applies to tasks created from now on)\n", shellJoin(c.Agent))
			return nil
		},
	}
	// Flags after COMMAND belong to the agent, not to tspace.
	cmd.Flags().SetInterspersed(false)
	cmd.Flags().BoolVar(&unset, "unset", false, "clear the default, so new tasks' root windows run a shell")
	return cmd
}

func (a *App) newConfigResumeCmd() *cobra.Command {
	var unset bool
	cmd := &cobra.Command{
		Use:   "resume [COMMAND [ARGS...] | --unset]",
		Short: "Show or set how the default agent resumes its conversation",
		Long: "With a command, new tasks that take the default agent from `tspace config\n" +
			"agent` record this beside it, for `tspace restart --resume`. A task given\n" +
			"another agent does not get it: it resumes a different program. Everything\n" +
			"after COMMAND is passed to it, and {context_dir} and {task_dir} work as for\n" +
			"the agent:\n\n" +
			"  tspace config agent claude --add-dir {context_dir}\n" +
			"  tspace config resume claude --continue --add-dir {context_dir}",
		RunE: func(cmd *cobra.Command, args []string) error {
			if unset && len(args) > 0 {
				return fmt.Errorf("choose either a command or --unset")
			}
			c, err := a.Store.LoadConfig()
			if err != nil {
				return err
			}
			switch {
			case unset:
				c.Resume = nil
			case len(args) == 0:
				a.printf("%s\n", resumeDisplay(c.Resume))
				return nil
			case strings.TrimSpace(args[0]) == "":
				return fmt.Errorf("resume command is empty; use --unset to clear it")
			default:
				c.Resume = args
			}
			if err := a.Store.SaveConfig(c); err != nil {
				return err
			}
			if unset {
				a.printf("cleared the default resume command\n")
				return nil
			}
			a.printf("default resume: %s (applies to tasks created from now on with the default agent)\n", shellJoin(c.Resume))
			if len(c.Agent) == 0 {
				a.warnf("note: no default agent is set, so no task gets this yet; set one with tspace config agent\n")
			}
			return nil
		},
	}
	// Flags after COMMAND belong to it, not to tspace.
	cmd.Flags().SetInterspersed(false)
	cmd.Flags().BoolVar(&unset, "unset", false, "clear the default resume command")
	return cmd
}

func defaultAgentDisplay(argv []string) string {
	if len(argv) == 0 {
		return "none (task roots run a shell)"
	}
	return shellJoin(argv)
}
