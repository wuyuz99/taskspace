// Package runtime owns persistent terminal sessions.
//
// A Runtime is one terminal multiplexer. tmux and zellij are built in (see
// registry.go); any other is reached through an external adapter speaking the
// protocol in adapter.go. Nothing outside this package should know which
// multiplexer is in use.
package runtime

import (
	"context"
	"fmt"
	"os"
	"strings"
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
// It deliberately stops at "is the process still alive". Semantic states like
// "waiting for input" are not knowable from tmux, and guessing them would make
// the status output untrustworthy.
type PaneState struct {
	Window  string
	Dead    bool
	Command string
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
	// State reports what is observably running.
	State(ctx context.Context, session string) (SessionState, error)
}

// launchArgv is the argv a window runs: its command, or for an empty Command
// the user's interactive shell (shell overrides $SHELL), behind env(1) when the
// window has environment of its own. env execs the command, so the pane's
// process is still the command itself.
func launchArgv(w Window, shell string) []string {
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
	if len(w.Env) == 0 {
		return argv
	}
	return append(append([]string{"env"}, w.Env...), argv...)
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
