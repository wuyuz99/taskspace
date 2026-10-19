// Package cli implements tspace's commands.
//
// This is the only package that formats anything for humans. git, runtime and
// status all return data; rendering happens here.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"github.com/spf13/cobra"
	tsexec "github.com/wuyuz99/taskspace/internal/exec"
	tsgit "github.com/wuyuz99/taskspace/internal/git"
	"github.com/wuyuz99/taskspace/internal/runtime"
	"github.com/wuyuz99/taskspace/internal/task"
)

// version is set at build time via -ldflags by release builds.
var version = "dev"

// buildVersion is the release version, or for `go install ...@v0.1.0`, which
// sets no ldflags, the module version Go recorded in the binary.
func buildVersion() string {
	if version != "dev" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return version
}

// App carries everything the commands need.
type App struct {
	Store   *task.Store
	Git     *tsgit.Git
	Runtime runtime.Runtime
	Out     io.Writer
	Err     io.Writer

	noTitle bool
	// noWait makes a command on a busy task fail rather than wait for it.
	noWait bool
	// settle is how long start waits before checking that what it launched
	// is still running.
	settle time.Duration
}

// launchSettle is long enough for an agent that dies on launch, or a session
// that goes with it, to have done so.
const launchSettle = time.Second

// NewApp wires the default implementations together.
func NewApp() (*App, error) {
	store, err := task.Open()
	if err != nil {
		return nil, err
	}
	runner := tsexec.Real()
	return &App{
		Store:   store,
		Git:     tsgit.New(runner),
		Runtime: configuredRuntime(store, runner),
		Out:     os.Stdout,
		Err:     os.Stderr,
		settle:  launchSettle,
	}, nil
}

// configuredRuntime is the multiplexer chosen by $TSPACE_MULTIPLEXER, else by
// the config, else tmux. A bad choice does not stop tspace from starting: the
// commands that need no session still work, and the rest report the problem.
func configuredRuntime(store *task.Store, runner tsexec.Runner) runtime.Runtime {
	c, err := store.LoadConfig()
	if err != nil {
		return runtime.Unavailable{Err: err}
	}
	name := multiplexerName(c)
	rt, err := runtime.New(name, c.Adapters[name], runner)
	if err != nil {
		return runtime.Unavailable{Err: err}
	}
	return rt
}

// multiplexerName is the multiplexer in effect.
func multiplexerName(c *task.Config) string {
	if env := os.Getenv("TSPACE_MULTIPLEXER"); env != "" {
		return env
	}
	if c.Multiplexer != "" {
		return c.Multiplexer
	}
	return runtime.Default
}

func (a *App) printf(format string, args ...any) {
	fmt.Fprintf(a.Out, format, args...)
}

func (a *App) warnf(format string, args ...any) {
	fmt.Fprintf(a.Err, format, args...)
}

// shortcut puts a subcommand at the top level under another name, for the
// commands typed often enough to deserve one. It is left out of the help, which
// lists each command once.
func shortcut(cmd *cobra.Command, name string) *cobra.Command {
	cmd.Use = name + strings.TrimPrefix(cmd.Use, cmd.Name())
	cmd.Aliases = nil
	cmd.Hidden = true
	return cmd
}

// warnNearerRoot warns when dir is inside a tspace root other than the one in
// use, where a command would quietly read and write state that is not the one
// around it. It does not switch to that root: which state a command touches
// stays the explicit choice of TSPACE_ROOT.
func (a *App) warnNearerRoot(dir string) {
	if within(dir, a.Store.Root()) {
		return
	}
	near := task.NearestRoot(dir)
	if near == "" || sameDir(near, a.Store.Root()) {
		return
	}
	a.warnf("tspace: note: this directory is in the tspace root %s, but tspace is using %s; set %s=%s to use the one here\n",
		near, a.Store.Root(), task.EnvRoot, near)
}

// within reports whether dir is root or below it, as written or with symlinks
// resolved; the written form is checked first because it costs nothing.
func within(dir, root string) bool {
	if isWithin(dir, root) {
		return true
	}
	rd, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return false
	}
	rr, err := filepath.EvalSymlinks(root)
	return err == nil && isWithin(rd, rr)
}

func isWithin(dir, root string) bool {
	rel, err := filepath.Rel(root, dir)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// sameDir reports whether two paths name one directory, through symlinks and
// case-insensitive file systems.
func sameDir(x, y string) bool {
	a, err := os.Stat(x)
	if err != nil {
		return false
	}
	b, err := os.Stat(y)
	return err == nil && os.SameFile(a, b)
}

// checksRoot reports whether cmd should warn about a nearer root: not shell
// completion, whose output the shell reads, nor help, nor a quiet signal.
func checksRoot(cmd *cobra.Command) bool {
	if cmd.Annotations[groupAnnotation] != "" {
		return false // it only shows help, or refuses
	}
	for c := cmd; c != nil; c = c.Parent() {
		if strings.HasPrefix(c.Name(), "__") || c.Name() == "completion" || c.Name() == "help" {
			return false
		}
	}
	quiet, err := cmd.Flags().GetBool("quiet")
	return err != nil || !quiet
}

// requireSubcommand makes every command that only groups others fail on an
// argument that names none of them, with cobra's suggestions, instead of
// printing help and exiting 0 as if a mistyped command had run.
//
// `GROUP help [COMMAND]` still shows help, as it did for a group cobra would
// not run.
func requireSubcommand(cmd *cobra.Command) {
	for _, c := range cmd.Commands() {
		requireSubcommand(c)
	}
	if cmd.HasParent() && cmd.HasAvailableSubCommands() && cmd.Run == nil && cmd.RunE == nil {
		cmd.Args = cobra.ArbitraryArgs
		cmd.DisableFlagsInUseLine = true
		if cmd.Annotations == nil {
			cmd.Annotations = map[string]string{}
		}
		cmd.Annotations[groupAnnotation] = "true"
		cmd.RunE = func(cmd *cobra.Command, args []string) error {
			switch {
			case len(args) == 0:
				return cmd.Help()
			case args[0] == "help":
				if c, _, err := cmd.Find(args[1:]); err == nil {
					return c.Help()
				}
				return cmd.Help()
			}
			return unknownSubcommand(cmd, args[0])
		}
	}
}

// groupAnnotation marks a command that only groups others, which runs only to
// show its help or refuse a word that names none of them.
const groupAnnotation = "tspace-group"

// noSubcommandArgs is cobra.NoArgs for a command that also has subcommands:
// a word after it is a mistyped one of them, and gets suggestions.
func noSubcommandArgs(cmd *cobra.Command, args []string) error {
	if len(args) > 0 {
		return unknownSubcommand(cmd, args[0])
	}
	return nil
}

func unknownSubcommand(cmd *cobra.Command, arg string) error {
	if cmd.SuggestionsMinimumDistance <= 0 {
		cmd.SuggestionsMinimumDistance = 2 // cobra's own, which it sets only on the root
	}
	msg := fmt.Sprintf("unknown command %q for %q", arg, cmd.CommandPath())
	if s := cmd.SuggestionsFor(arg); len(s) > 0 {
		msg += "\n\nDid you mean this?\n\t" + strings.Join(s, "\n\t")
	}
	return errors.New(msg)
}

// loadTask is the common "resolve the task argument" step.
func (a *App) loadTask(name string) (*task.Task, error) { return a.Store.Load(name) }

// listed unwraps a listing error: entries the store had to skip are reported as
// warnings and the remaining rows are still shown, because one unreadable file
// should not make `tspace task list` refuse to tell you about the other nine.
func (a *App) listed(err error) error {
	var skipped *task.SkippedError
	if errors.As(err, &skipped) {
		for _, e := range skipped.Errs {
			a.warnf("tspace: %v\n", e)
		}
		return nil
	}
	return err
}

// Execute builds and runs the command tree.
func Execute(ctx context.Context) int {
	app, err := NewApp()
	if err != nil {
		fmt.Fprintf(os.Stderr, "tspace: %v\n", err)
		return 1
	}

	root := &cobra.Command{
		Use:   "tspace",
		Short: "Manage multi-repo, multi-worktree coding-agent tasks",
		Long: "tspace manages multi-repo, multi-worktree coding-agent tasks.\n\n" +
			"Worktrees are per-repository. Features aren't. A task is the first-class\n" +
			"object: it spans repositories, each with its own branch, worktree and agent.\n\n" +
			"Requires git, and a terminal multiplexer for sessions (Linux, macOS or\n" +
			"WSL): tmux by default, or zellij or another chosen with `tspace config\n" +
			"multiplexer`. Without one everything but the session still works: use\n" +
			"`tspace start TASK --no-runtime`.\n\n" +
			"A typical task:\n\n" +
			"  tspace config repo backend ~/src/backend     register a repository, once\n" +
			"  tspace task create PAY-142 backend frontend  record the task; nothing on disk yet\n" +
			"  tspace start PAY-142                         create worktrees, start the session\n" +
			"  tspace status PAY-142                        what is running, what is dirty\n" +
			"  tspace attach PAY-142                        enter the session\n" +
			"  tspace stop PAY-142                          end the session, keep the worktrees\n" +
			"  tspace clean PAY-142                         remove the worktrees, if nothing is lost\n" +
			"  tspace task rm PAY-142                       delete the record and its notes\n\n" +
			"`tspace open TASK` is start and attach in one step, and `tspace dash` watches\n" +
			"every task at once.\n\n" +
			"State lives in $TSPACE_ROOT, by default ~/.tspace:\n\n" +
			"  tasks/TASK.json                     the task record\n" +
			"  worktrees/TASK/                     the task root, where the root agent runs\n" +
			"  worktrees/TASK/REPO/                one worktree per repository\n" +
			"  worktrees/TASK/.context/TASK.md     the brief: what the task is\n" +
			"  worktrees/TASK/.context/HANDOFF.md  the handoff: where the work stands\n\n" +
			"Run inside another root, a command warns that it is not using it.\n" +
			"The windows tspace opens get TSPACE_ROOT set to the root that started them.\n\n" +
			"For scripts and agents: open, attach, shell, dash and `task brief --edit`\n" +
			"take over the terminal and need a TTY, and window switches to the new\n" +
			"window unless given --detach. Every other command runs to completion,\n" +
			"prints its result on stdout and warnings on stderr, and exits non-zero on\n" +
			"failure. status, task list and task show take --json. An agent working\n" +
			"in a worktree finds its instructions in ../.context/TASK.md, records\n" +
			"progress with `tspace task handoff TASK --file FILE`, and says when it is\n" +
			"blocked or done with `tspace signal`.",
		Version:       buildVersion(),
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentPreRun = func(cmd *cobra.Command, _ []string) {
		if !checksRoot(cmd) {
			return
		}
		if cwd, err := os.Getwd(); err == nil {
			app.warnNearerRoot(cwd)
		}
	}
	root.PersistentFlags().BoolVar(&app.noTitle, "no-title", false,
		"do not set the terminal tab title")

	root.AddCommand(
		app.newInitCmd(),
		app.newConfigCmd(),
		app.newTaskCmd(),
		app.newProjectCmd(),
		app.newRepoCmd(),
		app.newStartCmd(),
		app.newOpenCmd(),
		app.newRestartCmd(),
		app.newStopCmd(),
		app.newAttachCmd(),
		app.newShellCmd(),
		app.newWindowCmd(),
		app.newPathCmd(),
		app.newExecCmd(),
		app.newStatusCmd(),
		app.newDashCmd(),
		app.newSignalCmd(),
		app.newCleanCmd(),
		app.newImportCmd(),
		app.newEditorCmd(),
		shortcut(app.newTaskCreateCmd(), "new"),
	)
	// cobra adds its completion group only on Execute; added now, it is
	// covered too.
	root.InitDefaultCompletionCmd()
	requireSubcommand(root)

	if err := root.ExecuteContext(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "tspace: %v\n", err)
		return 1
	}
	return 0
}

func (a *App) newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Create the tspace state directories and report where they live",
		Long: "init creates the state directories. It is never required — every command\n" +
			"creates what it needs — but it is a convenient way to see where tspace keeps\n" +
			"its files. They default to ~/.tspace; set TSPACE_ROOT to keep them elsewhere.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := a.Store.Init(); err != nil {
				return err
			}
			a.printf("state root:  %s\n", a.Store.Root())
			a.printf("tasks:       %s\n", a.Store.TasksDir())
			a.printf("worktrees:   %s\n", a.Store.WorktreesDir())
			a.printf("clones:      %s (made by config repo NAME URL)\n", a.Store.ReposDir())
			a.printf("config:      %s\n", a.Store.ConfigPath())
			if os.Getenv(task.EnvRoot) == "" {
				a.printf("\nSet TSPACE_ROOT to keep workspaces somewhere else.\n")
			}
			return nil
		},
	}
}
