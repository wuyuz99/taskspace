package cli

import (
	"errors"
	"fmt"
	"os"
	osexec "os/exec"

	"github.com/spf13/cobra"
)

func (a *App) newShellCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "shell TASK [REPO]",
		Short: "Open an interactive shell in a task's worktree",
		Long: "shell starts your shell in the worktree, with the terminal tab labelled.\n" +
			"It is the ad-hoc 'let me poke at this by hand' command, so you never have\n" +
			"to navigate to a worktree yourself.\n\n" +
			"REPO may be omitted when the task has only one repository.",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			t, err := a.loadTask(args[0])
			if err != nil {
				return err
			}
			repoArg := ""
			if len(args) == 2 {
				repoArg = args[1]
			}
			r, name, err := a.worktreeDir(t, repoArg)
			if err != nil {
				return err
			}

			setTitle(a.Out, fmt.Sprintf("aw: %s/%s", t.Name, name), a.noTitle)

			sh := os.Getenv("SHELL")
			if sh == "" {
				sh = "/bin/sh"
			}
			a.warnf("aw: %s/%s in %s\n", t.Name, name, r.Worktree)

			shell := osexec.CommandContext(cmd.Context(), sh)
			shell.Dir = r.Worktree
			shell.Stdin, shell.Stdout, shell.Stderr = os.Stdin, os.Stdout, os.Stderr
			shell.Env = append(os.Environ(),
				"AW_TASK="+t.Name,
				"AW_REPO="+name,
				"AW_WORKTREE="+r.Worktree,
			)
			if err := shell.Run(); err != nil {
				// A shell exiting non-zero is how shells exit; it is not an aw error.
				var exit *osexec.ExitError
				if errors.As(err, &exit) {
					return nil
				}
				return err
			}
			return nil
		},
	}
}

func (a *App) newPathCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "path TASK [REPO]",
		Short: "Print a worktree path",
		Long: "path prints the worktree path and nothing else, so it composes:\n\n" +
			"  cd \"$(aw path checkout-redesign backend)\"",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			t, err := a.loadTask(args[0])
			if err != nil {
				return err
			}
			repoArg := ""
			if len(args) == 2 {
				repoArg = args[1]
			}
			// Deliberately not worktreeDir: printing a path should work even
			// before `aw start` has created it.
			names := t.RepoNames()
			if len(names) == 0 {
				return fmt.Errorf("task %q has no repositories", t.Name)
			}
			if repoArg == "" {
				if len(names) > 1 {
					return fmt.Errorf("task %q has several repos (%s); name one", t.Name, joinNames(names))
				}
				repoArg = names[0]
			}
			r, ok := t.Repo(repoArg)
			if !ok {
				return fmt.Errorf("task %q has no repo named %q (has: %s)", t.Name, repoArg, joinNames(names))
			}
			a.printf("%s\n", r.Worktree)
			return nil
		},
	}
}

func (a *App) newExecCmd() *cobra.Command {
	var keepGoing bool

	cmd := &cobra.Command{
		Use:   "exec TASK -- COMMAND [ARGS...]",
		Short: "Run a command in every worktree of a task",
		Long: "exec runs the same command in each of the task's worktrees, in repo-name\n" +
			"order. Useful for the things you would otherwise do in each tab by hand:\n\n" +
			"  aw exec checkout-redesign -- git push\n" +
			"  aw exec checkout-redesign -- git status --short",
		// One arg, not two, so that a missing command produces our own message
		// rather than cobra's "requires at least 2 arg(s)".
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			t, err := a.loadTask(args[0])
			if err != nil {
				return err
			}
			argv := args[1:]
			if len(argv) == 0 {
				return fmt.Errorf("no command given; use: aw exec %s -- COMMAND", t.Name)
			}

			names := t.RepoNames()
			if len(names) == 0 {
				return fmt.Errorf("task %q has no repositories", t.Name)
			}

			var failed []string
			for _, name := range names {
				r := t.Repos[name]
				if info, statErr := os.Stat(r.Worktree); statErr != nil || !info.IsDir() {
					a.warnf("=== %s: worktree missing, skipped\n", name)
					failed = append(failed, name)
					continue
				}

				a.printf("=== %s\n", name)
				sub := osexec.CommandContext(cmd.Context(), argv[0], argv[1:]...)
				sub.Dir = r.Worktree
				sub.Stdin, sub.Stdout, sub.Stderr = os.Stdin, a.Out, a.Err
				if err := sub.Run(); err != nil {
					a.warnf("=== %s: %v\n", name, err)
					failed = append(failed, name)
					if !keepGoing {
						break
					}
				}
			}

			if len(failed) > 0 {
				return fmt.Errorf("failed in: %s", joinNames(failed))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&keepGoing, "keep-going", true,
		"continue after a repository fails; --keep-going=false stops at the first failure")
	return cmd
}
