package cli

import (
	"context"
	"fmt"
	"os"
	osexec "os/exec"
	"strings"

	"github.com/wuyuz99/taskspace/internal/task"
)

// openInNewTab runs `tspace open TASK` in a new tab of the user's terminal,
// with the command from `tspace config terminal`.
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
	argv := terminalArgv(c.Terminal, []string{self, "open", taskName})
	cmd := osexec.CommandContext(ctx, argv[0], argv[1:]...)
	// The state this dash shows, for terminals whose new tabs inherit the
	// environment; others start them from their own, as they would any tab.
	cmd.Env = append(os.Environ(), task.EnvRoot+"="+a.Store.Root())
	if out, err := cmd.CombinedOutput(); err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("%s: %s", argv[0], msg)
	}
	return nil
}

// terminalArgv is the terminal command to run command with: command added at
// the end, or, where an argument holds {command}, put there as one shell
// string.
func terminalArgv(terminal, command []string) []string {
	argv := append([]string(nil), terminal...)
	placed := false
	for i, arg := range argv {
		if strings.Contains(arg, "{command}") {
			argv[i] = strings.ReplaceAll(arg, "{command}", shellJoin(command))
			placed = true
		}
	}
	if !placed {
		argv = append(argv, command...)
	}
	return argv
}
