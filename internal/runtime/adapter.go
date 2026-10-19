package runtime

import (
	"context"

	"fmt"
	"os"
	osexec "os/exec"
	"strings"

	tsexec "github.com/wuyuz99/taskspace/internal/exec"
)

// AdapterProtocol is the version of the adapter protocol tspace speaks. It is
// passed to `check`, so an adapter can refuse a protocol it does not know.
const AdapterProtocol = "1"

// AdapterPrefix names the program a multiplexer without a configured command
// is looked up as: multiplexer "wezterm" runs tspace-mux-wezterm from PATH.
const AdapterPrefix = "tspace-mux-"

// Adapter implements Runtime by running an external program, so a multiplexer
// tspace has no built-in support for can be added without changing tspace.
//
// The adapter only translates operations into its multiplexer's commands. The
// safety rules — never start a window twice, never relaunch a live one — are
// enforced here, from the adapter's own state report, so an adapter cannot get
// them wrong. Each operation is one invocation:
//
//	ADAPTER check PROTOCOL           exit 0 if usable; otherwise explain on stderr
//	ADAPTER state SESSION            print "exists" or "missing", then a line
//	                                 per window: NAME<TAB>live|dead<TAB>COMMAND
//	ADAPTER new-session SESSION WIN  create a detached session running WIN
//	ADAPTER new-window SESSION WIN   add WIN to an existing session
//	ADAPTER respawn SESSION WIN      relaunch WIN, whose command has exited
//	ADAPTER stop SESSION             end the session, letting processes exit cleanly
//	ADAPTER attach SESSION           take over the terminal (stdin/stdout/stderr inherited)
//	ADAPTER focus SESSION NAME       show NAME if tspace runs inside a client; print "yes" if it did
//	ADAPTER select SESSION NAME      make NAME the window a later attach lands on
//	ADAPTER rename SESSION OLD NEW   rename window OLD to NEW (optional: an
//	                                 adapter without it fails, and only
//	                                 renaming windows is unavailable)
//
// WIN is the arguments NAME DIR keep|close -- COMMAND...: COMMAND is never
// empty (tspace resolves the user's shell), and keep says the window must stay
// open, showing its output, after COMMAND exits, where close lets it close.
// SESSION and NAME only contain [A-Za-z0-9@_~-]. Any non-zero exit is a
// failure, reported to the user with the adapter's stderr. Nothing needs
// parsing beyond positional arguments, so an adapter can be a shell script;
// docs/tspace-mux-tmux.sh is one.
type Adapter struct {
	name string
	argv []string
	run  tsexec.Runner
	// Shell overrides the interactive shell used for agent-less windows.
	Shell string
}

// NewAdapter returns a runtime for multiplexer name that runs argv; an empty
// argv means tspace-mux-NAME from PATH.
func NewAdapter(name string, argv []string, r tsexec.Runner) *Adapter {
	if r == nil {
		r = tsexec.Real()
	}
	if len(argv) == 0 {
		argv = []string{AdapterPrefix + name}
	}
	return &Adapter{name: name, argv: append([]string(nil), argv...), run: r}
}

func (a *Adapter) call(ctx context.Context, args ...string) (tsexec.Result, error) {
	return a.run.Run(ctx, "", append(append([]string(nil), a.argv...), args...)...)
}

// windowCall runs an operation that takes WIN.
func (a *Adapter) windowCall(ctx context.Context, op, session string, w Window) error {
	keep := "keep"
	if w.Transient {
		keep = "close"
	}
	args := []string{op, session, WindowName(w.Name), w.Dir, keep, "--"}
	_, err := a.call(ctx, append(args, launchArgv(w, a.Shell)...)...)
	return err
}

// Available implements Runtime.
func (a *Adapter) Available(ctx context.Context) error {
	if _, err := a.call(ctx, "check", AdapterProtocol); err != nil {
		if !tsexec.IsExit(err) {
			return fmt.Errorf("multiplexer %q has no usable adapter (%w); put %s%s on PATH, or set its command with: tspace config multiplexer %s COMMAND...", a.name, err, AdapterPrefix, a.name, a.name)
		}
		return fmt.Errorf("multiplexer %q is not usable: %w", a.name, err)
	}
	return nil
}

// SessionName implements Runtime.
func (a *Adapter) SessionName(task string) string { return sessionName(task) }

// WindowName implements Runtime.
func (a *Adapter) WindowName(repo string) string { return WindowName(repo) }

// HasSession implements Runtime.
func (a *Adapter) HasSession(ctx context.Context, session string) (bool, error) {
	st, err := a.State(ctx, session)
	return st.Exists, err
}

// State implements Runtime.
func (a *Adapter) State(ctx context.Context, session string) (SessionState, error) {
	st := SessionState{Name: session, Panes: map[string]PaneState{}}
	res, err := a.call(ctx, "state", session)
	if err != nil {
		return st, err
	}
	lines := strings.Split(strings.TrimRight(res.Stdout, "\r\n"), "\n")
	switch strings.TrimSpace(lines[0]) {
	case "missing":
		return st, nil
	case "exists":
		st.Exists = true
	default:
		return st, fmt.Errorf("%s state: first line is %q, want exists or missing", a.argv[0], lines[0])
	}
	for _, raw := range lines[1:] {
		line := strings.TrimRight(raw, "\r")
		if line == "" {
			continue
		}
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) < 2 || (fields[1] != "live" && fields[1] != "dead") {
			return st, fmt.Errorf("%s state: bad window line %q, want NAME<TAB>live|dead<TAB>COMMAND", a.argv[0], line)
		}
		name, dead, command := fields[0], fields[1] == "dead", ""
		if len(fields) == 3 {
			command = fields[2]
		}
		// Several lines for one name are split panes: alive if any is.
		if prev, ok := st.Panes[name]; ok && !prev.Dead {
			continue
		}
		st.Panes[name] = PaneState{Window: name, Dead: dead, Command: command}
	}
	return st, nil
}

// Start implements Runtime. Only missing windows are created, so it is safe
// to call repeatedly.
func (a *Adapter) Start(ctx context.Context, session string, windows []Window) error {
	if len(windows) == 0 {
		return fmt.Errorf("no windows to start for session %s", session)
	}
	st, err := a.State(ctx, session)
	if err != nil {
		return err
	}
	if !st.Exists {
		if err := a.windowCall(ctx, "new-session", session, windows[0]); err != nil {
			return fmt.Errorf("create session %s: %w", session, err)
		}
		windows = windows[1:]
	}
	for _, w := range missingWindows(st, windows) {
		if err := a.windowCall(ctx, "new-window", session, w); err != nil {
			return fmt.Errorf("create window %s in %s: %w", w.Name, session, err)
		}
	}
	return nil
}

// OpenWindow implements Runtime.
func (a *Adapter) OpenWindow(ctx context.Context, session string, w Window) error {
	st, err := a.State(ctx, session)
	if err != nil {
		return err
	}
	if err := checkOpen(st, session, w); err != nil {
		return err
	}
	if err := a.windowCall(ctx, "new-window", session, w); err != nil {
		return fmt.Errorf("create window %s in %s: %w", w.Name, session, err)
	}
	return nil
}

// Restart implements Runtime.
func (a *Adapter) Restart(ctx context.Context, session string, w Window) error {
	st, err := a.State(ctx, session)
	if err != nil {
		return err
	}
	if err := checkRestart(st, w); err != nil {
		return err
	}
	return a.windowCall(ctx, "respawn", session, w)
}

// Stop implements Runtime.
func (a *Adapter) Stop(ctx context.Context, session string) error {
	exists, err := a.HasSession(ctx, session)
	if err != nil || !exists {
		return err
	}
	if _, err := a.call(ctx, "stop", session); err != nil {
		return fmt.Errorf("stop session %s: %w", session, err)
	}
	return nil
}

// Attach implements Runtime. Like the tmux runtime, it bypasses the Runner to
// hand the adapter the terminal.
func (a *Adapter) Attach(ctx context.Context, session string) error {
	exists, err := a.HasSession(ctx, session)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("no %s session %q; run tspace start first", a.name, session)
	}
	argv := append(append([]string(nil), a.argv...), "attach", session)
	cmd := osexec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("attach to %s: %w", session, err)
	}
	return nil
}

// Focus implements Runtime.
func (a *Adapter) Focus(ctx context.Context, session, window string) (bool, error) {
	res, err := a.call(ctx, "focus", session, WindowName(window))
	if err != nil {
		return false, fmt.Errorf("switch to %s in %s: %w", window, session, err)
	}
	return strings.TrimSpace(res.Stdout) == "yes", nil
}

// Select implements Runtime.
func (a *Adapter) Select(ctx context.Context, session, window string) error {
	if _, err := a.call(ctx, "select", session, WindowName(window)); err != nil {
		return fmt.Errorf("select %s in %s: %w", window, session, err)
	}
	return nil
}

// RenameWindow implements Runtime.
func (a *Adapter) RenameWindow(ctx context.Context, session, from, to string) error {
	if _, err := a.call(ctx, "rename", session, WindowName(from), WindowName(to)); err != nil {
		return fmt.Errorf("rename %s to %s in %s (an adapter may not support renaming): %w", from, to, session, err)
	}
	return nil
}
