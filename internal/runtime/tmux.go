package runtime

import (
	"context"
	"encoding/hex"
	"fmt"
	"os"
	osexec "os/exec"
	"strings"
	"time"

	awexec "github.com/wuyuz99/taskspace/internal/exec"
)

// Tmux implements Runtime on top of tmux.
type Tmux struct {
	run awexec.Runner
	// StopGrace is how long processes get after SIGINT before the session is
	// killed. A hard kill can land mid-edit, so it is worth waiting.
	StopGrace time.Duration
	// Shell overrides the interactive shell used for agent-less windows.
	Shell string
}

// NewTmux returns a tmux runtime.
func NewTmux(r awexec.Runner) *Tmux {
	if r == nil {
		r = awexec.Real()
	}
	return &Tmux{run: r, StopGrace: 2 * time.Second}
}

func (t *Tmux) tmux(ctx context.Context, args ...string) (awexec.Result, error) {
	return t.run.Run(ctx, "", append([]string{"tmux"}, args...)...)
}

// Available implements Runtime.
func (t *Tmux) Available(ctx context.Context) error {
	if _, err := t.tmux(ctx, "-V"); err != nil {
		return fmt.Errorf("tmux is required but not usable: %w", err)
	}
	return nil
}

// SessionName implements Runtime.
func (t *Tmux) SessionName(task string) string { return "aw@" + sanitize(task) }
func (t *Tmux) WindowName(repo string) string  { return WindowName(repo) }

// target builds an exact-match tmux target. The '=' prefix disables tmux's
// prefix matching, without which "aw-pay" would match session "aw-payments".
func target(session string) string { return "=" + session }

func windowTarget(session, window string) string { return "=" + session + ":=" + window }

// HasSession implements Runtime.
func (t *Tmux) HasSession(ctx context.Context, session string) (bool, error) {
	_, err := t.tmux(ctx, "has-session", "-t", target(session))
	if err == nil {
		return true, nil
	}
	if awexec.IsExit(err) {
		return false, nil
	}
	return false, err
}

// Start implements Runtime.
//
// It is safe to call repeatedly: an existing session is kept, and only missing
// windows are added. Restarting must never discard a running agent.
func (t *Tmux) Start(ctx context.Context, session string, windows []Window) error {
	if len(windows) == 0 {
		return fmt.Errorf("no windows to start for session %s", session)
	}
	if err := t.checkLegacySession(ctx, session); err != nil {
		return err
	}

	// One State call, not one per window: each hasWindow probe costs two tmux
	// invocations, and the answer for every window is in the same list-panes.
	st, err := t.State(ctx, session)
	if err != nil {
		return err
	}

	if !st.Exists {
		first := windows[0]
		args := []string{"new-session", "-d", "-s", session, "-n", WindowName(first.Name)}
		if first.Dir != "" {
			args = append(args, "-c", first.Dir)
		}
		// Create a shell first so even an immediately exiting agent gets
		// remain-on-exit before it runs. Only this fresh shell is replaced.
		args = append(args, "/bin/sh")
		if _, err := t.tmux(ctx, args...); err != nil {
			return fmt.Errorf("create tmux session %s: %w", session, err)
		}
		if err := t.setRemainOnExit(ctx, session, first.Name); err != nil {
			return err
		}
		if err := t.launchFresh(ctx, session, first); err != nil {
			return err
		}
		windows = windows[1:]
	}

	for _, w := range windows {
		if _, present := st.Panes[WindowName(w.Name)]; present {
			continue
		}
		if err := t.openWindow(ctx, session, w); err != nil {
			return err
		}
	}
	return nil
}

// OpenWindow implements Runtime.
//
// The existence check is not redundant with the caller's: tmux allows two
// windows to share a name, so creating a duplicate and then respawning the
// target would kill whatever the *first* window is running — an agent, most
// likely. Refuse instead, and let restart be the way to relaunch a window.
func (t *Tmux) OpenWindow(ctx context.Context, session string, w Window) error {
	present, err := t.hasWindow(ctx, session, w.Name)
	if err != nil {
		return err
	}
	if present {
		return fmt.Errorf("window %s already exists in %s; use restart to relaunch it", w.Name, session)
	}
	return t.openWindow(ctx, session, w)
}

// openWindow creates the window unconditionally. Callers must already know the
// name is free.
func (t *Tmux) openWindow(ctx context.Context, session string, w Window) error {
	args := []string{"new-window", "-d", "-t", target(session), "-n", WindowName(w.Name)}
	if w.Dir != "" {
		args = append(args, "-c", w.Dir)
	}
	args = append(args, "/bin/sh")

	if _, err := t.tmux(ctx, args...); err != nil {
		return fmt.Errorf("create tmux window %s in %s: %w", w.Name, session, err)
	}
	if w.Transient {
		if err := t.fixName(ctx, session, w.Name); err != nil {
			return err
		}
	} else if err := t.setRemainOnExit(ctx, session, w.Name); err != nil {
		return err
	}
	return t.launchFresh(ctx, session, w)
}

func (t *Tmux) launchFresh(ctx context.Context, session string, w Window) error {
	args := []string{"respawn-window", "-k", "-t", windowTarget(session, WindowName(w.Name))}
	if w.Dir != "" {
		args = append(args, "-c", w.Dir)
	}
	args = append(args, t.launchArgv(w)...)
	_, err := t.tmux(ctx, args...)
	return err
}

// Restart never uses -k: tmux itself refuses to replace a live pane even if
// another process starts one after our observation. Old dead-pane output is replaced.
func (t *Tmux) Restart(ctx context.Context, session string, w Window) error {
	if err := t.checkLegacySession(ctx, session); err != nil {
		return err
	}
	st, err := t.State(ctx, session)
	if err != nil {
		return err
	}
	if !st.Exists {
		return fmt.Errorf("session is missing; run aw start for this task first")
	}
	p, ok := st.Panes[WindowName(w.Name)]
	if !ok {
		return fmt.Errorf("window %s is missing; run aw start for this repository first", w.Name)
	}
	if !p.Dead {
		return fmt.Errorf("refusing to restart %s: a pane is still running", w.Name)
	}
	if err := t.setRemainOnExit(ctx, session, w.Name); err != nil {
		return err
	}
	args := []string{"respawn-window", "-t", windowTarget(session, WindowName(w.Name))}
	if w.Dir != "" {
		args = append(args, "-c", w.Dir)
	}
	args = append(args, t.launchArgv(w)...)
	_, err = t.tmux(ctx, args...)
	return err
}

// Old names were ambiguous. Never adopt or kill an old session automatically,
// and refuse launching duplicate agents while a possible legacy session exists.
func (t *Tmux) checkLegacySession(ctx context.Context, session string) error {
	if !strings.HasPrefix(session, "aw@") {
		return nil
	}
	s := strings.TrimPrefix(session, "aw@")
	var old strings.Builder
	old.WriteString("aw-")
	for i := 0; i < len(s); i++ {
		if s[i] == '~' && i+2 < len(s) {
			if _, err := hex.DecodeString(s[i+1 : i+3]); err == nil {
				old.WriteByte('-')
				i += 2
				continue
			}
		}
		old.WriteByte(s[i])
	}
	exists, err := t.HasSession(ctx, old.String())
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("legacy session %q exists; inspect it with tmux and stop or rename it manually before starting %q (old names may belong to another task)", old.String(), session)
	}
	return nil
}

// setRemainOnExit keeps a pane visible after its command exits.
//
// Without this the window closes the instant an agent crashes, taking the
// traceback with it — precisely when the user needs to read it. It is set per
// window rather than globally so aw never changes the behaviour of the user's
// other tmux sessions.
func (t *Tmux) setRemainOnExit(ctx context.Context, session, window string) error {
	if err := t.fixName(ctx, session, window); err != nil {
		return err
	}
	_, err := t.tmux(ctx, "set-option", "-t", windowTarget(session, WindowName(window)), "-w", "remain-on-exit", "on")
	if err != nil {
		return fmt.Errorf("set remain-on-exit on %s:%s: %w", session, window, err)
	}
	return nil
}

// fixName stops tmux renaming the window after its running command: every
// later target addresses the window by the name aw gave it.
func (t *Tmux) fixName(ctx context.Context, session, window string) error {
	_, err := t.tmux(ctx, "set-option", "-t", windowTarget(session, WindowName(window)), "-w", "automatic-rename", "off")
	return err
}

// Focus implements Runtime.
//
// Only a client inside tmux can be pointed at the window; from a plain
// terminal there is nothing to switch, and attaching would take the terminal
// over, which is not what asking for a window means.
func (t *Tmux) Focus(ctx context.Context, session, window string) (bool, error) {
	if os.Getenv("TMUX") == "" {
		return false, nil
	}
	if _, err := t.tmux(ctx, "switch-client", "-t", windowTarget(session, WindowName(window))); err != nil {
		return false, fmt.Errorf("switch to %s in %s: %w", window, session, err)
	}
	return true, nil
}

// launchArgv renders the command for a window. tmux takes the command as
// trailing arguments; an empty Command means an interactive shell.
func (t *Tmux) launchArgv(w Window) []string {
	if len(w.Command) == 0 {
		return []string{t.shell()}
	}
	return w.Command
}

func (t *Tmux) shell() string {
	if t.Shell != "" {
		return t.Shell
	}
	if s := os.Getenv("SHELL"); s != "" {
		return s
	}
	return "/bin/sh"
}

func (t *Tmux) hasWindow(ctx context.Context, session, window string) (bool, error) {
	st, err := t.State(ctx, session)
	if err != nil {
		return false, err
	}
	_, ok := st.Panes[WindowName(window)]
	return ok, nil
}

// Stop implements Runtime.
//
// Processes get SIGINT and a grace period before the session is killed, because
// killing an agent outright can land in the middle of a file write.
func (t *Tmux) Stop(ctx context.Context, session string) error {
	exists, err := t.HasSession(ctx, session)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}

	// Pane ids, not window targets: tmux resolves a window target to its active
	// pane, so a split opened to probe something would never see the interrupt
	// and would be hard-killed below.
	panes, err := t.livePanes(ctx, session)
	if err != nil {
		return err
	}
	interrupted := false
	for _, pane := range panes {
		if _, err := t.tmux(ctx, "send-keys", "-t", pane, "C-c"); err == nil {
			interrupted = true
		}
	}

	if interrupted && t.StopGrace > 0 {
		select {
		case <-time.After(t.StopGrace):
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	if _, err := t.tmux(ctx, "kill-session", "-t", target(session)); err != nil {
		// A session that ended on its own during the grace period is a success,
		// not a failure.
		if ok, checkErr := t.HasSession(ctx, session); checkErr == nil && !ok {
			return nil
		}
		return fmt.Errorf("stop session %s: %w", session, err)
	}
	return nil
}

// Attach implements Runtime.
//
// This is the one place that bypasses the Runner: attaching means handing the
// terminal to tmux, and the Runner captures output instead of inheriting it.
// Inside an existing tmux client, attaching would nest, so we switch instead.
func (t *Tmux) Attach(ctx context.Context, session string) error {
	exists, err := t.HasSession(ctx, session)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("no tmux session %q; run aw start first", session)
	}

	args := []string{"attach-session", "-t", target(session)}
	if os.Getenv("TMUX") != "" {
		args = []string{"switch-client", "-t", target(session)}
	}

	cmd := osexec.CommandContext(ctx, "tmux", args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("attach to %s: %w", session, err)
	}
	return nil
}

// livePaneFormat lists individual panes rather than windows, for the one
// caller that must reach every process in a session.
const livePaneFormat = "#{pane_id}\t#{pane_dead}"

// livePanes returns the pane ids of every pane whose command is still running.
// Pane ids are unique across the tmux server, so they need no session prefix.
func (t *Tmux) livePanes(ctx context.Context, session string) ([]string, error) {
	res, err := t.tmux(ctx, "list-panes", "-s", "-t", target(session), "-F", livePaneFormat)
	if err != nil {
		if awexec.IsExit(err) {
			// The session went away between the check and here.
			return nil, nil
		}
		return nil, err
	}
	var ids []string
	for _, raw := range strings.Split(res.Stdout, "\n") {
		line := strings.TrimRight(raw, "\r")
		if line == "" {
			continue
		}
		id, dead, ok := strings.Cut(line, "\t")
		if !ok || id == "" || dead == "1" {
			continue
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// stateFormat is a tmux format string. Formats are a stable interface; the
// human-readable output of list-panes is not, and must never be parsed.
const stateFormat = "#{window_name}\t#{pane_dead}\t#{pane_current_command}"

// State implements Runtime.
func (t *Tmux) State(ctx context.Context, session string) (SessionState, error) {
	st := SessionState{Name: session, Panes: map[string]PaneState{}}

	exists, err := t.HasSession(ctx, session)
	if err != nil {
		return st, err
	}
	if !exists {
		return st, nil
	}
	st.Exists = true

	res, err := t.tmux(ctx, "list-panes", "-s", "-t", target(session), "-F", stateFormat)
	if err != nil {
		if awexec.IsExit(err) {
			// The session went away between the two calls.
			st.Exists = false
			return st, nil
		}
		return st, err
	}
	for name, pane := range parsePanes(res.Stdout) {
		st.Panes[name] = pane
	}
	return st, nil
}

// parsePanes reads the tab-separated stateFormat output.
//
// A window with several panes collapses to one entry: the window is considered
// alive if any of its panes is alive, since a split opened for probing should
// not make the agent look dead.
func parsePanes(out string) map[string]PaneState {
	panes := map[string]PaneState{}
	for _, raw := range strings.Split(out, "\n") {
		line := strings.TrimRight(raw, "\r")
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 2 {
			continue
		}
		name := fields[0]
		dead := fields[1] == "1"
		command := ""
		if len(fields) > 2 {
			command = fields[2]
		}

		if prev, ok := panes[name]; ok {
			if !prev.Dead || !dead {
				dead = false
			}
			if command == "" {
				command = prev.Command
			}
		}
		panes[name] = PaneState{Window: name, Dead: dead, Command: command}
	}
	return panes
}
