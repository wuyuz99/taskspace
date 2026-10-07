// Package cli implements aw's commands.
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

	"github.com/spf13/cobra"
	awexec "github.com/wuyuz99/taskspace/internal/exec"
	awgit "github.com/wuyuz99/taskspace/internal/git"
	"github.com/wuyuz99/taskspace/internal/runtime"
	"github.com/wuyuz99/taskspace/internal/task"
)

// version is set at build time via -ldflags.
var version = "dev"

// App carries everything the commands need.
type App struct {
	Store   *task.Store
	Git     *awgit.Git
	Runtime runtime.Runtime
	Out     io.Writer
	Err     io.Writer

	noTitle bool
}

// NewApp wires the default implementations together.
func NewApp() (*App, error) {
	store, err := task.Open()
	if err != nil {
		return nil, err
	}
	runner := awexec.Real()
	return &App{
		Store:   store,
		Git:     awgit.New(runner),
		Runtime: runtime.NewTmux(runner),
		Out:     os.Stdout,
		Err:     os.Stderr,
	}, nil
}

func (a *App) printf(format string, args ...any) {
	fmt.Fprintf(a.Out, format, args...)
}

func (a *App) warnf(format string, args ...any) {
	fmt.Fprintf(a.Err, format, args...)
}

// loadTask is the common "resolve the task argument" step.
func (a *App) loadTask(name string) (*task.Task, error) { return a.Store.Load(name) }

// listed unwraps a listing error: entries the store had to skip are reported as
// warnings and the remaining rows are still shown, because one unreadable file
// should not make `aw task list` refuse to tell you about the other nine.
func (a *App) listed(err error) error {
	var skipped *task.SkippedError
	if errors.As(err, &skipped) {
		for _, e := range skipped.Errs {
			a.warnf("aw: %v\n", e)
		}
		return nil
	}
	return err
}

// Execute builds and runs the command tree.
func Execute(ctx context.Context) int {
	app, err := NewApp()
	if err != nil {
		fmt.Fprintf(os.Stderr, "aw: %v\n", err)
		return 1
	}

	root := &cobra.Command{
		Use:   "aw",
		Short: "Manage multi-repo, multi-worktree coding-agent tasks",
		Long: "aw manages multi-repo, multi-worktree coding-agent tasks.\n\n" +
			"Worktrees are per-repository. Features aren't. A task is the first-class\n" +
			"object: it spans repositories, each with its own branch, worktree and agent.\n\n" +
			"Requires git, and tmux for sessions (Linux, macOS or WSL). Without tmux\n" +
			"everything but the session still works: use `aw start TASK --no-runtime`.\n\n" +
			"A typical task:\n\n" +
			"  aw config repo backend ~/src/backend     register a repository, once\n" +
			"  aw task create PAY-142 backend frontend  record the task; nothing on disk yet\n" +
			"  aw start PAY-142                         create worktrees, start the session\n" +
			"  aw status PAY-142                        what is running, what is dirty\n" +
			"  aw attach PAY-142                        enter the session\n" +
			"  aw stop PAY-142                          end the session, keep the worktrees\n" +
			"  aw clean PAY-142                         remove the worktrees, if nothing is lost\n" +
			"  aw task rm PAY-142                       delete the record and its notes\n\n" +
			"`aw open TASK` is start and attach in one step.\n\n" +
			"State lives in $AW_ROOT, by default ~/.aw:\n\n" +
			"  tasks/TASK.json                     the task record\n" +
			"  worktrees/TASK/                     the task root, where the root agent runs\n" +
			"  worktrees/TASK/REPO/                one worktree per repository\n" +
			"  worktrees/TASK/.context/TASK.md     the brief: what the task is\n" +
			"  worktrees/TASK/.context/HANDOFF.md  the handoff: where the work stands\n\n" +
			"For scripts and agents: open, attach, shell and `task brief --edit` take\n" +
			"over the terminal and need a TTY, and window switches to the new window\n" +
			"unless given --detach. Every other command runs to completion, prints its\n" +
			"result on stdout and warnings on stderr, and exits non-zero on failure.\n" +
			"status, task list and task show take --json. An agent working in a\n" +
			"worktree finds its instructions in ../.context/TASK.md and records\n" +
			"progress with `aw task handoff TASK --file FILE`.",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
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
		app.newCleanCmd(),
	)

	if err := root.ExecuteContext(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "aw: %v\n", err)
		return 1
	}
	return 0
}

func (a *App) newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Create the aw state directories and report where they live",
		Long: "init creates the state directories. It is never required — every command\n" +
			"creates what it needs — but it is a convenient way to see where aw keeps\n" +
			"its files. They default to ~/.aw; set AW_ROOT to keep them elsewhere.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := a.Store.Init(); err != nil {
				return err
			}
			a.printf("state root:  %s\n", a.Store.Root())
			a.printf("tasks:       %s\n", a.Store.TasksDir())
			a.printf("worktrees:   %s\n", a.Store.WorktreesDir())
			a.printf("config:      %s\n", a.Store.ConfigPath())
			if os.Getenv("AW_ROOT") == "" {
				a.printf("\nSet AW_ROOT to keep workspaces somewhere else.\n")
			}
			return nil
		},
	}
}
