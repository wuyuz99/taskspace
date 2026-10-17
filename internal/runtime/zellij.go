package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	tsexec "github.com/wuyuz99/taskspace/internal/exec"
)

// Zellij implements Runtime on top of zellij (0.44 or later, for
// `action list-panes` and per-pane targeting).
//
// zellij's model maps onto tspace's directly: a session per task, a tab per
// window, and command panes that are held open after their command exits,
// which is what tmux's remain-on-exit gives the tmux runtime. Two zellij
// behaviours shape this file:
//
//   - A tab added to a session no client is attached to is created with no
//     size and is dropped when a client attaches, orphaning its process. So a
//     new session is created from a layout holding every window, and tabs are
//     only added to sessions someone is attached to.
//   - A held pane reruns its command when it receives Enter, which works
//     without a client, so restart reruns in place instead of replacing the tab.
type Zellij struct {
	run tsexec.Runner
	// StopGrace is how long processes get after Ctrl-C before the session is
	// killed.
	StopGrace time.Duration
	// Shell overrides the interactive shell used for agent-less windows.
	Shell string

	mu       sync.Mutex
	selected map[string]string // session -> window for the next Attach
}

// NewZellij returns a zellij runtime.
func NewZellij(r tsexec.Runner) *Zellij {
	if r == nil {
		r = tsexec.Real()
	}
	return &Zellij{run: r, StopGrace: 2 * time.Second}
}

func (z *Zellij) zellij(ctx context.Context, args ...string) (tsexec.Result, error) {
	return z.run.Run(ctx, "", append([]string{"zellij"}, args...)...)
}

// action runs a CLI action against one session. The session is always named:
// inside zellij, an unnamed action would go to whichever session tspace is
// running in.
func (z *Zellij) action(ctx context.Context, session string, args ...string) (tsexec.Result, error) {
	return z.zellij(ctx, append([]string{"--session", session, "action"}, args...)...)
}

// Available implements Runtime.
func (z *Zellij) Available(ctx context.Context) error {
	if _, err := z.zellij(ctx, "--version"); err != nil {
		return fmt.Errorf("zellij is required but not usable: %w", err)
	}
	// list-panes is the newest action this runtime depends on.
	if _, err := z.zellij(ctx, "action", "list-panes", "--help"); err != nil {
		return fmt.Errorf("zellij 0.44 or later is required (no `zellij action list-panes`): %w", err)
	}
	return nil
}

// SessionName implements Runtime.
func (z *Zellij) SessionName(task string) string { return sessionName(task) }

// WindowName implements Runtime.
func (z *Zellij) WindowName(repo string) string { return WindowName(repo) }

// sessionEntry is one line of `zellij list-sessions`.
type sessionEntry struct {
	exited bool
}

// sessions lists every session zellij knows about, including exited ones kept
// for resurrection.
func (z *Zellij) sessions(ctx context.Context) (map[string]sessionEntry, error) {
	res, err := z.zellij(ctx, "list-sessions", "--no-formatting")
	if err != nil {
		if tsexec.IsExit(err) && strings.Contains(res.Stdout+res.Stderr, "No active zellij sessions") {
			return map[string]sessionEntry{}, nil
		}
		return nil, err
	}
	return parseSessions(res.Stdout), nil
}

// parseSessions reads lines such as
//
//	tspace@task [Created 3m ago]
//	tspace@old [Created 2h ago] (EXITED - attach to resurrect)
//
// Session names tspace creates never contain spaces, so the name is the first
// field.
func parseSessions(out string) map[string]sessionEntry {
	found := map[string]sessionEntry{}
	for _, raw := range strings.Split(out, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		name, _, _ := strings.Cut(line, " ")
		found[name] = sessionEntry{exited: strings.Contains(line, "(EXITED")}
	}
	return found
}

// HasSession implements Runtime. An exited session is not running anything,
// so it does not count.
func (z *Zellij) HasSession(ctx context.Context, session string) (bool, error) {
	all, err := z.sessions(ctx)
	if err != nil {
		return false, err
	}
	e, ok := all[session]
	return ok && !e.exited, nil
}

// Start implements Runtime.
//
// Safe to call repeatedly: an existing session is kept and only missing tabs
// are added. An exited session of the same name is deleted first, because
// attaching to it would resurrect its old layout instead of creating ours.
func (z *Zellij) Start(ctx context.Context, session string, windows []Window) error {
	if len(windows) == 0 {
		return fmt.Errorf("no windows to start for session %s", session)
	}
	all, err := z.sessions(ctx)
	if err != nil {
		return err
	}
	if e, ok := all[session]; !ok || e.exited {
		if ok {
			if _, err := z.zellij(ctx, "delete-session", session); err != nil {
				return fmt.Errorf("delete exited zellij session %s: %w", session, err)
			}
		}
		return z.create(ctx, session, windows)
	}

	st, err := z.State(ctx, session)
	if err != nil {
		return err
	}
	missing := missingWindows(st, windows)
	if len(missing) == 0 {
		return nil
	}
	if err := z.requireClient(ctx, session, "add "+missing[0].Name); err != nil {
		return err
	}
	for _, w := range missing {
		if err := z.newTab(ctx, session, w); err != nil {
			return err
		}
	}
	return nil
}

// create starts a detached session from a layout holding every window.
func (z *Zellij) create(ctx context.Context, session string, windows []Window) error {
	f, err := os.CreateTemp("", "tspace-zellij-*.kdl")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, werr := f.WriteString(z.layout(windows))
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return werr
	}
	if _, err := z.detached(ctx, "--layout", f.Name(), "attach", "--create-background", session); err != nil {
		return fmt.Errorf("create zellij session %s: %w", session, err)
	}
	// attach returns before the server has read the layout, and a server that
	// finds no layout file quietly uses the default one, so the file must
	// outlive the tabs' creation. Tab names show up before their panes do, so
	// the wait is for every window's pane: until then State would report the
	// windows missing, and a second Start would try to add them again. The
	// launch delay keeps even a pane that closes on exit around long enough to
	// be seen.
	for deadline := time.Now().Add(layoutTimeout); ; {
		panes, exists, err := z.panes(ctx, session)
		if err == nil && len(missingWindows(paneState(session, panes, exists), windows)) == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("zellij session %s started without tspace's layout; inspect it, then run: zellij kill-session %s", session, session)
		}
		select {
		case <-time.After(50 * time.Millisecond):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// detached runs zellij as if from outside any session. Run from a zellij pane,
// zellij would otherwise treat a new session as nested in the current one.
func (z *Zellij) detached(ctx context.Context, args ...string) (tsexec.Result, error) {
	if _, inside := insideZellij(); !inside {
		return z.zellij(ctx, args...)
	}
	argv := append([]string{"env", "-u", "ZELLIJ", "-u", "ZELLIJ_SESSION_NAME", "-u", "ZELLIJ_PANE_ID", "zellij"}, args...)
	return z.run.Run(ctx, "", argv...)
}

// layoutTimeout bounds the wait for a new session to apply its layout.
const layoutTimeout = 10 * time.Second

// layout renders windows as a zellij layout, keeping the tab and status bars
// of zellij's default layout so the session looks like any other.
func (z *Zellij) layout(windows []Window) string {
	var b strings.Builder
	b.WriteString("layout {\n")
	b.WriteString("    default_tab_template {\n")
	b.WriteString("        pane size=1 borderless=true {\n            plugin location=\"zellij:tab-bar\"\n        }\n")
	b.WriteString("        children\n")
	b.WriteString("        pane size=2 borderless=true {\n            plugin location=\"zellij:status-bar\"\n        }\n")
	b.WriteString("    }\n")
	for _, w := range windows {
		argv := z.launchArgv(w)
		fmt.Fprintf(&b, "    tab name=%s", kdlString(WindowName(w.Name)))
		if w.Dir != "" {
			fmt.Fprintf(&b, " cwd=%s", kdlString(w.Dir))
		}
		b.WriteString(" {\n")
		fmt.Fprintf(&b, "        pane command=%s {\n", kdlString(argv[0]))
		if len(argv) > 1 {
			b.WriteString("            args")
			for _, a := range argv[1:] {
				b.WriteString(" " + kdlString(a))
			}
			b.WriteString("\n")
		}
		if w.Transient {
			b.WriteString("            close_on_exit true\n")
		}
		b.WriteString("        }\n    }\n")
	}
	b.WriteString("}\n")
	return b.String()
}

// kdlString quotes s as a KDL string.
func kdlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u{%x}`, r)
				continue
			}
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// requireClient refuses to add a tab to a session nobody is attached to; see
// the type comment for why.
func (z *Zellij) requireClient(ctx context.Context, session, what string) error {
	n, err := z.clients(ctx, session)
	if err != nil || n > 0 {
		return err
	}
	return fmt.Errorf("cannot %s: zellij can only add a tab to a session with a client attached; attach first (tspace attach), then run this again", what)
}

// clients counts the clients attached to session.
func (z *Zellij) clients(ctx context.Context, session string) (int, error) {
	res, err := z.action(ctx, session, "list-clients")
	if err != nil {
		return 0, fmt.Errorf("list clients of %s: %w", session, err)
	}
	// The first line is a header.
	return len(strings.Split(strings.TrimSpace(res.Stdout), "\n")) - 1, nil
}

func (z *Zellij) newTab(ctx context.Context, session string, w Window) error {
	args := []string{"new-tab", "--no-focus", "--name", WindowName(w.Name)}
	if w.Dir != "" {
		args = append(args, "--cwd", w.Dir)
	}
	if w.Transient {
		args = append(args, "--close-on-exit")
	}
	args = append(args, "--")
	args = append(args, z.launchArgv(w)...)
	if _, err := z.action(ctx, session, args...); err != nil {
		return fmt.Errorf("create zellij tab %s in %s: %w", w.Name, session, err)
	}
	return nil
}

// OpenWindow implements Runtime.
func (z *Zellij) OpenWindow(ctx context.Context, session string, w Window) error {
	st, err := z.State(ctx, session)
	if err != nil {
		return err
	}
	if err := checkOpen(st, session, w); err != nil {
		return err
	}
	if err := z.requireClient(ctx, session, "open "+w.Name); err != nil {
		return err
	}
	return z.newTab(ctx, session, w)
}

// Restart implements Runtime.
//
// A held pane still running the window's launch command, directory included
// (see launchArgv), is rerun in place with Enter. When either has changed
// since, the tab is replaced, which, like any new tab, needs a client attached.
func (z *Zellij) Restart(ctx context.Context, session string, w Window) error {
	panes, exists, err := z.panes(ctx, session)
	if err != nil {
		return err
	}
	if err := checkRestart(paneState(session, panes, exists), w); err != nil {
		return err
	}
	name := WindowName(w.Name)
	var tab []zellijPane
	for _, p := range panes {
		if p.TabName == name {
			tab = append(tab, p)
		}
	}

	want := strings.Join(z.launchArgv(w), " ")
	for _, p := range tab {
		if p.Held && p.TerminalCommand == want {
			_, err := z.action(ctx, session, "send-keys", "--pane-id", p.target(), "Enter")
			return err
		}
	}

	if err := z.requireClient(ctx, session, "restart "+w.Name+" with a changed command or directory"); err != nil {
		return err
	}
	if _, err := z.action(ctx, session, "close-tab-by-id", fmt.Sprint(tab[0].TabID)); err != nil {
		return fmt.Errorf("close tab %s in %s: %w", w.Name, session, err)
	}
	return z.newTab(ctx, session, w)
}

// Stop implements Runtime. Every live pane gets Ctrl-C and a grace period
// before the session is killed; the session is then deleted, so it is not
// kept around for resurrection.
func (z *Zellij) Stop(ctx context.Context, session string) error {
	panes, exists, err := z.panes(ctx, session)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	interrupted := false
	for _, p := range panes {
		if p.Exited {
			continue
		}
		if _, err := z.action(ctx, session, "send-keys", "--pane-id", p.target(), "Ctrl c"); err == nil {
			interrupted = true
		}
	}
	if interrupted && z.StopGrace > 0 {
		select {
		case <-time.After(z.StopGrace):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if _, err := z.zellij(ctx, "kill-session", session); err != nil {
		if ok, checkErr := z.HasSession(ctx, session); checkErr != nil || ok {
			return fmt.Errorf("stop session %s: %w", session, err)
		}
	}
	// Without resurrection enabled there is nothing left to delete.
	_, _ = z.zellij(ctx, "delete-session", session)
	return nil
}

// insideZellij returns the session tspace is running inside, if any.
func insideZellij() (string, bool) {
	if os.Getenv("ZELLIJ") == "" {
		return "", false
	}
	return os.Getenv("ZELLIJ_SESSION_NAME"), true
}

// Attach implements Runtime. Inside zellij the current client switches
// sessions instead of nesting, landing on the tab Select chose.
func (z *Zellij) Attach(ctx context.Context, session string) error {
	exists, err := z.HasSession(ctx, session)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("no zellij session %q; run tspace start first", session)
	}
	window := z.takeSelected(session)
	if current, inside := insideZellij(); inside {
		if current == session {
			// Select already switched this client's tab.
			return nil
		}
		args := []string{"action", "switch-session", session}
		if window != "" {
			if pos, ok, err := z.tabPosition(ctx, session, window); err == nil && ok {
				args = append(args, "--tab-position", fmt.Sprint(pos))
			}
		}
		if _, err := z.zellij(ctx, args...); err != nil {
			return fmt.Errorf("switch to %s: %w", session, err)
		}
		return nil
	}
	cmd := osexec.CommandContext(ctx, "zellij", "attach", session)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if window != "" {
		// Clients already attached elsewhere are not ours: wait for one more.
		if before, err := z.clients(ctx, session); err == nil {
			fctx, cancel := context.WithCancel(ctx)
			defer cancel()
			go z.focusWhenAttached(fctx, session, window, before)
		}
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("attach to %s: %w", session, err)
	}
	return nil
}

// Focus implements Runtime.
func (z *Zellij) Focus(ctx context.Context, session, window string) (bool, error) {
	current, inside := insideZellij()
	if !inside {
		return false, nil
	}
	name := WindowName(window)
	// go-to-tab-name succeeds even when no tab has the name, so the tab is
	// looked up first either way.
	pos, ok, err := z.tabPosition(ctx, session, name)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, fmt.Errorf("window %s is missing in %s", window, session)
	}
	if current == session {
		_, err = z.action(ctx, session, "go-to-tab-name", name)
	} else {
		_, err = z.zellij(ctx, "action", "switch-session", session, "--tab-position", fmt.Sprint(pos))
	}
	if err != nil {
		return false, fmt.Errorf("switch to %s in %s: %w", window, session, err)
	}
	return true, nil
}

// tabPosition returns the position of the tab named name (already a
// WindowName), and whether there is one.
func (z *Zellij) tabPosition(ctx context.Context, session, name string) (int, bool, error) {
	panes, _, err := z.panes(ctx, session)
	if err != nil {
		return 0, false, err
	}
	for _, p := range panes {
		if p.TabName == name {
			return p.TabPosition, true, nil
		}
	}
	return 0, false, nil
}

// Select implements Runtime.
//
// Switching tabs in a session with no client attached does not decide where
// the next client lands, so the choice is also remembered and applied by
// Attach once its client has connected.
func (z *Zellij) Select(ctx context.Context, session, window string) error {
	if _, err := z.action(ctx, session, "go-to-tab-name", WindowName(window)); err != nil {
		return fmt.Errorf("select %s in %s: %w", window, session, err)
	}
	z.mu.Lock()
	defer z.mu.Unlock()
	if z.selected == nil {
		z.selected = map[string]string{}
	}
	z.selected[session] = WindowName(window)
	return nil
}

// takeSelected returns and forgets the window Select chose for session.
func (z *Zellij) takeSelected(session string) string {
	z.mu.Lock()
	defer z.mu.Unlock()
	w := z.selected[session]
	delete(z.selected, session)
	return w
}

// focusWhenAttached switches to window once more than before clients are
// attached, giving up after a few seconds: landing on the wrong tab is not
// worth failing the attach over.
func (z *Zellij) focusWhenAttached(ctx context.Context, session, window string, before int) {
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		select {
		case <-time.After(100 * time.Millisecond):
		case <-ctx.Done():
			return
		}
		if n, err := z.clients(ctx, session); err == nil && n > before {
			_, _ = z.action(ctx, session, "go-to-tab-name", window)
			return
		}
	}
}

// zellijPane is the part of `zellij action list-panes --json` tspace reads.
// The JSON is zellij's machine interface; the table output is not.
type zellijPane struct {
	ID              int    `json:"id"`
	IsPlugin        bool   `json:"is_plugin"`
	Exited          bool   `json:"exited"`
	Held            bool   `json:"is_held"`
	TabID           int    `json:"tab_id"`
	TabPosition     int    `json:"tab_position"`
	TabName         string `json:"tab_name"`
	PaneCommand     string `json:"pane_command"`
	TerminalCommand string `json:"terminal_command"`
}

func (p zellijPane) target() string { return fmt.Sprintf("terminal_%d", p.ID) }

// panes returns the session's terminal panes, and whether the session exists;
// plugin panes such as the tab bar are not windows. Sessions are only listed
// when list-panes fails, which is how a missing or exited session shows up.
func (z *Zellij) panes(ctx context.Context, session string) ([]zellijPane, bool, error) {
	res, err := z.action(ctx, session, "list-panes", "--all", "--json")
	if err != nil {
		if ok, checkErr := z.HasSession(ctx, session); checkErr == nil && !ok {
			return nil, false, nil
		}
		return nil, true, err
	}
	panes, err := parseZellijPanes(res.Stdout)
	return panes, true, err
}

func parseZellijPanes(out string) ([]zellijPane, error) {
	var all []zellijPane
	if err := json.Unmarshal([]byte(out), &all); err != nil {
		return nil, fmt.Errorf("parse zellij list-panes output: %w", err)
	}
	terminals := all[:0]
	for _, p := range all {
		if !p.IsPlugin {
			terminals = append(terminals, p)
		}
	}
	return terminals, nil
}

// State implements Runtime. As with tmux, a tab with several panes is alive
// if any of them is.
func (z *Zellij) State(ctx context.Context, session string) (SessionState, error) {
	panes, exists, err := z.panes(ctx, session)
	if err != nil {
		return SessionState{Name: session, Panes: map[string]PaneState{}}, err
	}
	return paneState(session, panes, exists), nil
}

// paneState groups panes into windows by tab.
func paneState(session string, panes []zellijPane, exists bool) SessionState {
	st := SessionState{Name: session, Exists: exists, Panes: map[string]PaneState{}}
	for _, p := range panes {
		command := ""
		if !p.Exited {
			command = commandName(p.PaneCommand)
		}
		if prev, ok := st.Panes[p.TabName]; ok {
			if command == "" {
				command = prev.Command
			}
			st.Panes[p.TabName] = PaneState{Window: p.TabName, Dead: prev.Dead && p.Exited, Command: command}
			continue
		}
		st.Panes[p.TabName] = PaneState{Window: p.TabName, Dead: p.Exited, Command: command}
	}
	return st
}

// commandName reduces zellij's full command line to the program name, which
// is what tmux reports as a pane's current command.
func commandName(cmdline string) string {
	fields := strings.Fields(cmdline)
	if len(fields) == 0 {
		return ""
	}
	return filepath.Base(fields[0])
}

// launchDelay holds every command back briefly before it runs. zellij can
// miss the exit of a command that dies while its pane is still being set up —
// about one in fifteen session starts — and then reports the pane as running
// forever, which would hide exactly the agent that crashed on launch.
const launchDelay = "0.25"

// launchArgv is the window's command behind the launch delay. The wrapper
// execs the command, so the pane's process is the command itself once it runs.
//
// The wrapper's $0 fingerprints the directory and the exact argv. zellij
// reports a pane's command space-joined and not its directory, so the
// fingerprint is what lets Restart tell a held pane would rerun what w asks for.
func (z *Zellij) launchArgv(w Window) []string {
	argv := launchArgv(w, z.Shell)
	h := sha256.New()
	h.Write([]byte(w.Dir))
	for _, a := range argv {
		h.Write([]byte{0})
		h.Write([]byte(a))
	}
	tag := fmt.Sprintf("tspace-%x", h.Sum(nil)[:6])
	return append([]string{"sh", "-c", "sleep " + launchDelay + `; exec "$@"`, tag}, argv...)
}
