// Package runtimetest is one in-memory multiplexer for tests, checked against
// the real ones by Conformance.
package runtimetest

import (
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"sync"

	"github.com/wuyuz99/taskspace/internal/runtime"
)

// Op names a Runtime method, for Fail and Calls.
type Op string

const (
	OpAvailable  Op = "Available"
	OpHasSession Op = "HasSession"
	OpStart      Op = "Start"
	OpOpenWindow Op = "OpenWindow"
	OpRestart    Op = "Restart"
	OpStop       Op = "Stop"
	OpAttach     Op = "Attach"
	OpFocus      Op = "Focus"
	OpSelect     Op = "Select"
	OpRename     Op = "RenameWindow"
	OpState      Op = "State"
	OpClose      Op = "CloseWindow"
)

// Call is one thing the fake did. Start records one per window it launched.
type Call struct {
	Op      Op
	Session string
	Window  runtime.Window
}

// Fake is a multiplexer held in memory. Unlike a real one, a command runs until
// a test calls Exit, except that `false` exits at once, so Conformance can run
// the same windows here and on a real multiplexer.
type Fake struct {
	mu       sync.Mutex
	sessions map[string]map[string]runtime.PaneState
	windows  map[string]map[string]runtime.Window
	errs     map[Op]error
	calls    []Call
	onStop   func()
	crashing map[string]bool
	inside   bool
}

// New returns a fake with no sessions.
func New() *Fake {
	return &Fake{
		sessions: map[string]map[string]runtime.PaneState{},
		windows:  map[string]map[string]runtime.Window{},
		errs:     map[Op]error{},
		crashing: map[string]bool{},
	}
}

// Fail makes op return err until it is given a nil one. Each operation fails
// on its own, so HasSession can answer while State cannot.
func (f *Fake) Fail(op Op, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.errs[op] = err
}

// Calls returns what the fake did, of the given operations, or all of it.
func (f *Fake) Calls(ops ...Op) []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Call
	for _, c := range f.calls {
		if len(ops) == 0 || contains(ops, c.Op) {
			out = append(out, c)
		}
	}
	return out
}

// ForgetCalls empties the record, so a test can look at what comes next.
func (f *Fake) ForgetCalls() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = nil
}

// OnStop runs fn as Stop begins, as an agent finishing up would.
func (f *Fake) OnStop(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.onStop = fn
}

// ExitAtStart makes these windows' commands exit as Start launches them.
func (f *Fake) ExitAtStart(windows ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, w := range windows {
		f.crashing[runtime.WindowName(w)] = true
	}
}

// ClientInside says tspace runs inside a client, so Focus can show a window.
func (f *Fake) ClientInside(inside bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inside = inside
}

// Window returns what an open window was launched with. A seeded one has only
// its name.
func (f *Fake) Window(session, window string) (runtime.Window, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := runtime.WindowName(window)
	if _, ok := f.sessions[session][id]; !ok {
		return runtime.Window{}, false
	}
	if w, ok := f.windows[session][id]; ok {
		return w, true
	}
	return runtime.Window{Name: window}, true
}

func contains(ops []Op, op Op) bool {
	for _, o := range ops {
		if o == op {
			return true
		}
	}
	return false
}

// Seed sets up a session with exactly these panes, keyed by window identifier,
// as a test that only reads state wants it.
func (f *Fake) Seed(session string, panes map[string]runtime.PaneState) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessions[session] = map[string]runtime.PaneState{}
	f.windows[session] = map[string]runtime.Window{}
	for name, p := range panes {
		p.Window = name
		f.sessions[session][name] = p
	}
}

// Exit ends window's command, as if it had finished or crashed. A transient
// window closes; any other stays open, dead.
func (f *Fake) Exit(session, window string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.exit(session, runtime.WindowName(window))
}

// Reboot is the machine restarting: every session is gone.
func (f *Fake) Reboot() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessions = map[string]map[string]runtime.PaneState{}
	f.windows = map[string]map[string]runtime.Window{}
}

func (f *Fake) exit(session, id string) {
	panes, ok := f.sessions[session]
	if !ok {
		return
	}
	p, ok := panes[id]
	if !ok {
		return
	}
	if f.windows[session][id].Transient {
		f.remove(session, id)
		return
	}
	p.Dead = true
	panes[id] = p
}

// remove closes a window. A session ends with its last window.
func (f *Fake) remove(session, id string) {
	delete(f.sessions[session], id)
	delete(f.windows[session], id)
	if len(f.sessions[session]) == 0 {
		delete(f.sessions, session)
		delete(f.windows, session)
	}
}

func (f *Fake) launch(session string, w runtime.Window) {
	id := runtime.WindowName(w.Name)
	command := "sh"
	if len(w.Command) > 0 {
		command = filepath.Base(w.Command[0])
	}
	f.sessions[session][id] = runtime.PaneState{Window: id, Command: command}
	f.windows[session][id] = w
	if command == "false" {
		f.exit(session, id)
	}
}

func (f *Fake) record(op Op, session string, w runtime.Window) {
	f.calls = append(f.calls, Call{Op: op, Session: session, Window: w})
}

// Available implements runtime.Runtime.
func (f *Fake) Available(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.errs[OpAvailable]
}

// SessionName implements runtime.Runtime, naming a session as the real ones do.
func (f *Fake) SessionName(task string) string { return "tspace@" + runtime.WindowName(task) }

// WindowName implements runtime.Runtime.
func (f *Fake) WindowName(repo string) string { return runtime.WindowName(repo) }

// HasSession implements runtime.Runtime.
func (f *Fake) HasSession(_ context.Context, session string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.errs[OpHasSession]; err != nil {
		return false, err
	}
	_, ok := f.sessions[session]
	return ok, nil
}

// Start implements runtime.Runtime.
func (f *Fake) Start(_ context.Context, session string, windows []runtime.Window) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.errs[OpStart]; err != nil {
		return err
	}
	if len(windows) == 0 {
		return fmt.Errorf("no windows to start for session %s", session)
	}
	if _, ok := f.sessions[session]; !ok {
		f.sessions[session] = map[string]runtime.PaneState{}
		f.windows[session] = map[string]runtime.Window{}
	}
	for _, w := range windows {
		if _, ok := f.sessions[session][runtime.WindowName(w.Name)]; !ok {
			f.record(OpStart, session, w)
			f.launch(session, w)
			if id := runtime.WindowName(w.Name); f.crashing[id] {
				f.exit(session, id)
			}
		}
	}
	return nil
}

// OpenWindow implements runtime.Runtime.
func (f *Fake) OpenWindow(_ context.Context, session string, w runtime.Window) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.errs[OpOpenWindow]; err != nil {
		return err
	}
	panes, ok := f.sessions[session]
	if !ok {
		return fmt.Errorf("session is missing; run tspace start for this task first")
	}
	if _, present := panes[runtime.WindowName(w.Name)]; present {
		return fmt.Errorf("window %s already exists in %s; use restart to relaunch it", w.Name, session)
	}
	f.record(OpOpenWindow, session, w)
	f.launch(session, w)
	return nil
}

// Restart implements runtime.Runtime.
func (f *Fake) Restart(_ context.Context, session string, w runtime.Window) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.errs[OpRestart]; err != nil {
		return err
	}
	panes, ok := f.sessions[session]
	if !ok {
		return fmt.Errorf("session is missing; run tspace start for this task first")
	}
	p, ok := panes[runtime.WindowName(w.Name)]
	if !ok {
		return fmt.Errorf("window %s is missing; run tspace start for this repository first", w.Name)
	}
	if !p.Dead {
		return fmt.Errorf("refusing to restart %s: a pane is still running", w.Name)
	}
	f.record(OpRestart, session, w)
	f.launch(session, w)
	return nil
}

// Stop implements runtime.Runtime. A session that is not there is stopped.
func (f *Fake) Stop(_ context.Context, session string) error {
	f.mu.Lock()
	onStop := f.onStop
	f.mu.Unlock()
	if onStop != nil {
		onStop()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.errs[OpStop]; err != nil {
		return err
	}
	f.record(OpStop, session, runtime.Window{})
	delete(f.sessions, session)
	delete(f.windows, session)
	return nil
}

// Attach implements runtime.Runtime.
func (f *Fake) Attach(_ context.Context, session string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.errs[OpAttach]; err != nil {
		return err
	}
	if _, ok := f.sessions[session]; !ok {
		return fmt.Errorf("no session %q; run tspace start first", session)
	}
	f.record(OpAttach, session, runtime.Window{})
	return nil
}

// Focus implements runtime.Runtime.
func (f *Fake) Focus(_ context.Context, session, window string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.errs[OpFocus]; err != nil {
		return false, err
	}
	f.record(OpFocus, session, runtime.Window{Name: window})
	return f.inside, nil
}

// Select implements runtime.Runtime.
func (f *Fake) Select(_ context.Context, session, window string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.errs[OpSelect]; err != nil {
		return err
	}
	if _, ok := f.sessions[session][runtime.WindowName(window)]; !ok {
		return fmt.Errorf("select %s in %s: no such window", window, session)
	}
	f.record(OpSelect, session, runtime.Window{Name: window})
	return nil
}

// RenameWindow implements runtime.Runtime.
func (f *Fake) RenameWindow(_ context.Context, session, from, to string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.errs[OpRename]; err != nil {
		return err
	}
	oldID, newID := runtime.WindowName(from), runtime.WindowName(to)
	p, ok := f.sessions[session][oldID]
	if !ok {
		return fmt.Errorf("rename %s to %s in %s: no such window", from, to, session)
	}
	w := f.windows[session][oldID]
	delete(f.sessions[session], oldID)
	delete(f.windows[session], oldID)
	p.Window, w.Name = newID, to
	f.sessions[session][newID] = p
	f.windows[session][newID] = w
	f.record(OpRename, session, w)
	return nil
}

// CloseWindow implements runtime.WindowCloser. A window that is not there is
// closed.
func (f *Fake) CloseWindow(_ context.Context, session, window string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.errs[OpClose]; err != nil {
		return err
	}
	f.record(OpClose, session, runtime.Window{Name: window})
	if _, ok := f.sessions[session][runtime.WindowName(window)]; ok {
		f.remove(session, runtime.WindowName(window))
	}
	return nil
}

// State implements runtime.Runtime. It returns a copy, as a real multiplexer's
// answer is: what happens later is not in it.
func (f *Fake) State(_ context.Context, session string) (runtime.SessionState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	st := runtime.SessionState{Name: session, Panes: map[string]runtime.PaneState{}}
	if err := f.errs[OpState]; err != nil {
		return st, err
	}
	if panes, ok := f.sessions[session]; ok {
		st.Exists = true
		st.Panes = maps.Clone(panes)
	}
	return st, nil
}

var (
	_ runtime.Runtime      = (*Fake)(nil)
	_ runtime.WindowCloser = (*Fake)(nil)
)
