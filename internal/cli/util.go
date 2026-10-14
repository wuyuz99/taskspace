package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// expandHome resolves a leading ~ so that paths typed in a shell that did not
// expand them still work.
func expandHome(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") && !strings.HasPrefix(path, `~\`) {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if path == "~" {
		return home
	}
	return filepath.Join(home, path[2:])
}

func joinNames(names []string) string { return strings.Join(names, ", ") }

// printJSON writes v for a --json flag. The field names are a contract with
// whatever is parsing them, unlike the tables, which are free to change.
func (a *App) printJSON(v any) error {
	enc := json.NewEncoder(a.Out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// shellJoin renders an argv for display, quoting only what needs it.
func shellJoin(argv []string) string {
	parts := make([]string, len(argv))
	for i, s := range argv {
		if s == "" || strings.ContainsAny(s, " \t\n\"'\\$`") {
			parts[i] = "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
		} else {
			parts[i] = s
		}
	}
	return strings.Join(parts, " ")
}

// setTitle labels the terminal tab with the task name.
//
// The user's top pain point is not knowing which tab belongs to which
// workspace. OSC 2 is honoured by Windows Terminal, iTerm2, WezTerm, Kitty and
// GNOME Terminal.
//
// It is emitted only to a real terminal: writing escape sequences into a pipe
// would corrupt whatever is reading it.
func setTitle(w io.Writer, title string, disabled bool) {
	if disabled || os.Getenv("TSPACE_NO_TITLE") != "" {
		return
	}
	if !isTerminal(w) {
		return
	}
	fmt.Fprintf(w, "\033]2;%s\007", title)
}

// isTerminal reports whether w is a character device.
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
