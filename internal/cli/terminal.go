package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	osexec "os/exec"
	goruntime "runtime"
	"strings"
	"time"

	"github.com/wuyuz99/taskspace/internal/git"
	"github.com/wuyuz99/taskspace/internal/task"
)

// launchWait is how long openInNewTab waits for the terminal command, to
// report one that fails at once. Many return as soon as the tab is open; some
// run until their window closes, which is not a failure.
const launchWait = 2 * time.Second

// openInNewTab attaches to the task's session in a new tab of the user's
// terminal, with the command from `tspace config terminal`.
func (a *App) openInNewTab(ctx context.Context, taskName string) error {
	c, err := a.Store.LoadConfig()
	if err != nil {
		return err
	}
	if len(c.Terminal) == 0 {
		return fmt.Errorf("no terminal is set for new tabs; set yours with tspace config terminal, e.g.: tspace config terminal wezterm cli spawn --")
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	// Carry the root in the command too: single-instance terminals launch
	// tabs from their existing server, which may have a different environment.
	command := []string{"env", task.EnvRoot + "=" + a.Store.Root(), self}
	if a.noTitle {
		command = append(command, "--no-title")
	}
	command = append(command, "attach", taskName)
	return a.launch(ctx, terminalArgv(c.Terminal, command))
}

// launch starts a program the user configured to open something of theirs,
// a terminal tab or an editor, and lets it be: it is not waited for, beyond
// launchWait to report one that fails at once, and neither ctx nor tspace
// exiting ends it. It gets newTabEnv's environment.
func (a *App) launch(ctx context.Context, argv []string) error {
	cmd := osexec.Command(argv[0], argv[1:]...)
	if cmd.Err != nil {
		return fmt.Errorf("%s not found (%v); change it with tspace config", argv[0], cmd.Err)
	}
	cmd.Env = newTabEnv(os.Environ(), a.Store.Root())
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	// A launcher's child may keep the output open for as long as it runs.
	cmd.WaitDelay = launchWait / 2
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("%s: %w", argv[0], err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			msg := strings.TrimSpace(out.String())
			if msg == "" {
				msg = err.Error()
			}
			return fmt.Errorf("%s: %s", argv[0], msg)
		}
	case <-time.After(launchWait):
	case <-ctx.Done():
	}
	return nil
}

// newTabEnv is the environment a new tab or editor is opened with: tspace's,
// without what says where tspace itself runs, which would make a tspace run
// in it switch this multiplexer client, or report for this task window. TSPACE_ROOT is the state dash shows, for terminals
// whose new tabs inherit the environment.
func newTabEnv(environ []string, root string) []string {
	drop := []string{"TMUX=", "TMUX_PANE=", "ZELLIJ=", "ZELLIJ_SESSION_NAME=", "ZELLIJ_PANE_ID=",
		envTask + "=", envWindow + "=", task.EnvLocked + "=", task.EnvRoot + "="}
	var env []string
	for _, kv := range git.RepositoryEnv(environ) {
		keep := true
		for _, d := range drop {
			if strings.HasPrefix(kv, d) {
				keep = false
			}
		}
		if keep {
			env = append(env, kv)
		}
	}
	return append(env, task.EnvRoot+"="+root)
}

// terminalArgv is the terminal command to run command with: command added at
// the end, or, where an argument holds {command}, put there as one command
// line, quoted for the platform's shell.
func terminalArgv(terminal, command []string) []string {
	return terminalArgvFor(goruntime.GOOS, terminal, command)
}

func terminalArgvFor(goos string, terminal, command []string) []string {
	line := shellJoin(command)
	if goos == "windows" {
		line = windowsJoin(command)
	}
	argv := append([]string(nil), terminal...)
	placed := false
	for i, arg := range argv {
		if strings.Contains(arg, "{command}") {
			argv[i] = strings.ReplaceAll(arg, "{command}", line)
			placed = true
		}
	}
	if !placed {
		argv = append(argv, command...)
	}
	return argv
}

// windowsJoin quotes argv as cmd.exe and CommandLineToArgvW read a command
// line: double quotes around an argument with spaces or quotes in it.
func windowsJoin(argv []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		if a == "" || strings.ContainsAny(a, " \t\"") {
			a = `"` + strings.ReplaceAll(a, `"`, `\"`) + `"`
		}
		parts[i] = a
	}
	return strings.Join(parts, " ")
}
