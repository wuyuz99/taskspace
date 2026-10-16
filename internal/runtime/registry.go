package runtime

import (
	"context"
	"fmt"
	"sort"

	tsexec "github.com/wuyuz99/taskspace/internal/exec"
)

// Default is the multiplexer used when none is configured.
const Default = "tmux"

// builtins are the multiplexers tspace drives directly. Adding one is a
// Runtime implementation and a line here; everything else reaches tspace
// through an Adapter.
var builtins = map[string]func(tsexec.Runner) Runtime{
	"tmux":   func(r tsexec.Runner) Runtime { return NewTmux(r) },
	"zellij": func(r tsexec.Runner) Runtime { return NewZellij(r) },
}

// Builtins returns the names of the built-in multiplexers, sorted.
func Builtins() []string {
	names := make([]string, 0, len(builtins))
	for n := range builtins {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// IsBuiltin reports whether name is a built-in multiplexer.
func IsBuiltin(name string) bool {
	_, ok := builtins[name]
	return ok
}

// ValidateName checks a multiplexer name, which becomes part of an adapter's
// program name.
func ValidateName(name string) error {
	if name == "" {
		return fmt.Errorf("multiplexer name is empty")
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' && i > 0) {
			return fmt.Errorf("invalid multiplexer name %q: use lowercase letters, digits and '-'", name)
		}
	}
	return nil
}

// New returns the runtime for multiplexer name. A built-in name is driven
// directly unless adapter overrides it; any other name runs adapter, or
// tspace-mux-NAME from PATH when adapter is empty.
func New(name string, adapter []string, r tsexec.Runner) (Runtime, error) {
	if name == "" {
		name = Default
	}
	if err := ValidateName(name); err != nil {
		return nil, err
	}
	if f, ok := builtins[name]; ok && len(adapter) == 0 {
		return f(r), nil
	}
	return NewAdapter(name, adapter, r), nil
}

// Unavailable is a runtime that cannot be used, because the configured one is
// invalid. Commands that need no session still work; the rest report err.
type Unavailable struct{ Err error }

func (u Unavailable) Available(context.Context) error                  { return u.Err }
func (u Unavailable) SessionName(task string) string                   { return sessionName(task) }
func (u Unavailable) WindowName(repo string) string                    { return WindowName(repo) }
func (u Unavailable) HasSession(context.Context, string) (bool, error) { return false, u.Err }
func (u Unavailable) Start(context.Context, string, []Window) error    { return u.Err }
func (u Unavailable) OpenWindow(context.Context, string, Window) error { return u.Err }
func (u Unavailable) Restart(context.Context, string, Window) error    { return u.Err }
func (u Unavailable) Stop(context.Context, string) error               { return u.Err }
func (u Unavailable) Attach(context.Context, string) error             { return u.Err }
func (u Unavailable) Select(context.Context, string, string) error     { return u.Err }
func (u Unavailable) Focus(context.Context, string, string) (bool, error) {
	return false, u.Err
}
func (u Unavailable) State(_ context.Context, session string) (SessionState, error) {
	return SessionState{Name: session, Panes: map[string]PaneState{}}, u.Err
}
