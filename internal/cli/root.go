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
			"object: it spans repositories, each with its own branch, worktree and agent.",
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
			"its files, and to override that with AW_ROOT.",
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
