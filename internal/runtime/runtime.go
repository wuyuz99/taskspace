// Package runtime owns persistent terminal sessions.
//
// A Runtime is one terminal multiplexer. tmux and zellij are built in (see
// registry.go); any other is reached through an external adapter speaking the
// protocol in adapter.go. Nothing outside this package should know which
// multiplexer is in use.
package runtime

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/wuyuz99/taskspace/internal/git"
)

// Window is one pane's worth of work: a named shell in a directory, optionally
// running a command.
type Window struct {
	Name string
	Dir  string
	// Command is the agent argv. Empty means the user's interactive shell.
	Command []string
	// Transient windows close when their process exits. Managed windows stay
	// open, dead, so a crashed agent's last output can still be read; an
	// ad-hoc shell the user typed `exit` into has nothing left to show.
	Transient bool
	// Env is extra environment for Command, as NAME=VALUE.
	Env []string
}

// PaneState is what we can actually observe about a running window.
//
// It holds only what the multiplexer itself tracks: whether the process is
// alive, whether it rang the bell, and when it last printed. Semantic states
// like "waiting for input" are not knowable from a multiplexer, and guessing
// them would make the status output untrustworthy; status may offer a long
// silence as a hint, labelled as one.
type PaneState struct {
	Window  string
	Dead    bool
	Command string
	// Bell says the window rang the terminal bell since a person last looked
	// at it: something in it wants attention.
	Bell bool
	// Output is when the window last printed anything. It is zero when the
	// multiplexer does not say.
	Output time.Time
}

// SessionState is the observed state of one task's session.
type SessionState struct {
	Name   string
	Exists bool
	Panes  map[string]PaneState
}

// Pane returns the state of a named window.
func (s SessionState) Pane(window string) (PaneState, bool) {
	p, ok := s.Panes[window]
	return p, ok
}

// Runtime manages persistent sessions.
type Runtime interface {
	// Available reports whether this runtime can be used at all.
	Available(ctx context.Context) error
	// SessionName maps a task name to this runtime's session identifier.
	SessionName(task string) string
	// WindowName maps a repository name to the backend's window identifier.
	WindowName(repo string) string
	// HasSession reports whether a session exists.
	HasSession(ctx context.Context, session string) (bool, error)
	// Start creates the session if absent and ensures every window exists.
	Start(ctx context.Context, session string, windows []Window) error
	// OpenWindow adds one window to an existing session.
	OpenWindow(ctx context.Context, session string, w Window) error
	// Restart relaunches a dead window, refusing if any pane is still alive.
	Restart(ctx context.Context, session string, w Window) error
	// Stop ends the session, giving processes a chance to exit cleanly first.
	Stop(ctx context.Context, session string) error
	// Attach hands the terminal over to the session.
	Attach(ctx context.Context, session string) error
	// Focus shows a window to the user when tspace runs inside a client that can
	// display it, and reports whether it did.
	Focus(ctx context.Context, session, window string) (bool, error)
	// Select makes a window the one a later Attach lands on.
	Select(ctx context.Context, session, window string) error
	// RenameWindow gives a window of a running session another name.
	RenameWindow(ctx context.Context, session, from, to string) error
	// State reports what is observably running.
	State(ctx context.Context, session string) (SessionState, error)
}

// mergePane folds p into what panes already holds for its window, for a
// window split into several panes: alive if any pane is, ringing if any is,
// and last printing when the latest did.
func mergePane(panes map[string]PaneState, p PaneState) PaneState {
	prev, ok := panes[p.Window]
	if !ok {
		return p
	}
	p.Dead = prev.Dead && p.Dead
	if p.Command == "" {
		p.Command = prev.Command
	}
	p.Bell = prev.Bell || p.Bell
	if prev.Output.After(p.Output) {
		p.Output = prev.Output
	}
	return p
}

// unixTime reads whole seconds since the epoch; anything else, the empty
// string included, is the zero time.
func unixTime(s string) time.Time {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil || n <= 0 {
		return time.Time{}
	}
	return time.Unix(n, 0)
}

// launchArgv is the argv a window runs: its command, or for an empty Command
// the user's interactive shell (shell overrides $SHELL), behind env(1) to clear
// repository context saved by an existing server and add the window environment.
// env execs the command, so the pane's process is still the command itself.
func launchArgv(w Window, shell string) []string {
	clean := []string{"env"}
	for _, key := range git.RepositoryEnvVars() {
		clean = append(clean, "-u", key)
	}
	return append(append(clean, w.Env...), commandArgv(w, shell)...)
}

func commandArgv(w Window, shell string) []string {
	var argv []string
	switch {
	case len(w.Command) > 0:
		argv = w.Command
	case shell != "":
		argv = []string{shell}
	case os.Getenv("SHELL") != "":
		argv = []string{os.Getenv("SHELL")}
	default:
		argv = []string{"/bin/sh"}
	}
	return argv
}

// sessionName is the session a task runs in. Every runtime uses it: tspace
// finds a task's existing session by this name, so the scheme must not differ
// between multiplexers.
func sessionName(task string) string { return "tspace@" + sanitize(task) }

// missingWindows returns the windows st has no pane for.
func missingWindows(st SessionState, windows []Window) []Window {
	var missing []Window
	for _, w := range windows {
		if _, present := st.Panes[WindowName(w.Name)]; !present {
			missing = append(missing, w)
		}
	}
	return missing
}

// checkOpen refuses to open w unless its session exists and has no window of
// that name. Multiplexers allow duplicate names, and every later lookup is by
// name, so a duplicate would make restart act on the wrong window.
func checkOpen(st SessionState, session string, w Window) error {
	if !st.Exists {
		return fmt.Errorf("session is missing; run tspace start for this task first")
	}
	if _, present := st.Panes[WindowName(w.Name)]; present {
		return fmt.Errorf("window %s already exists in %s; use restart to relaunch it", w.Name, session)
	}
	return nil
}

// checkRestart refuses to restart w unless it exists and nothing in it is
// still running: restarting must never discard a live agent.
func checkRestart(st SessionState, w Window) error {
	if !st.Exists {
		return fmt.Errorf("session is missing; run tspace start for this task first")
	}
	p, ok := st.Panes[WindowName(w.Name)]
	if !ok {
		return fmt.Errorf("window %s is missing; run tspace start for this repository first", w.Name)
	}
	if !p.Dead {
		return fmt.Errorf("refusing to restart %s: a pane is still running", w.Name)
	}
	return nil
}

// WindowName returns the window identifier a repo is given, so that callers
// outside this package (status, in particular) can look a repo's pane back up
// without duplicating the sanitizing rules.
func WindowName(repo string) string { return sanitize(repo) }

// DisplayName undoes WindowName, for a window found in a session whose name
// tspace did not choose itself, such as an extra window opened unsaved.
// An identifier sanitize did not produce is returned as it is.
func DisplayName(id string) string {
	var b strings.Builder
	for i := 0; i < len(id); i++ {
		if id[i] == '~' && i+2 < len(id) {
			if c, err := strconv.ParseUint(id[i+1:i+3], 16, 8); err == nil {
				b.WriteByte(byte(c))
				i += 2
				continue
			}
		}
		b.WriteByte(id[i])
	}
	return b.String()
}

// sanitize makes a string safe to use as a session or window identifier.
//
// tmux reads '.' and ':' as target syntax (session:window.pane), so a task
// named "checkout.redesign" would break every -t argument we construct. Task
// names permit dots because repositories legitimately have them, so the
// substitution happens here instead.
//
// Escape bytes, including the escape marker itself, so identities never collapse.
func sanitize(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range []byte(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteByte(r)
		case r == '-' || r == '_':
			b.WriteByte(r)
		default:
			fmt.Fprintf(&b, "~%02x", r)
		}
	}
	return b.String()
}

// NotInstalled reports whether err says the multiplexer, or its adapter, is
// not installed at all. Then it runs no sessions tspace could act on, which is
// not true of any other failure: a check can fail while sessions still run.
func NotInstalled(err error) bool {
	return errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist)
}
