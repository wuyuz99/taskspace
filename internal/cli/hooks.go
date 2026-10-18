package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"

	"github.com/spf13/cobra"
)

// claudeSignalHooks are the Claude Code hook events tspace listens to, in the
// order they are written, and the signal each one sends.
var claudeSignalHooks = []struct{ event, command string }{
	{"UserPromptSubmit", "tspace signal --quiet working"},
	{"Notification", "tspace signal --quiet blocked"},
	{"Stop", "tspace signal --quiet done"},
}

// claudeHookGroup is one entry of an event's list in Claude Code settings,
// with its fields in the order Claude Code's own documentation shows them.
type claudeHookGroup struct {
	Hooks []claudeHook `json:"hooks"`
}

type claudeHook struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

// signalHookRe recognises a hook tspace installed, or one written by hand
// from the README, whatever path tspace is called by.
var signalHookRe = regexp.MustCompile(`(^|[/\\\s])tspace(\.exe)?\s+signal(\s|$)`)

func (a *App) newConfigHooksCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hooks",
		Short: "Install the hooks that let agents report their state",
		Long: "hooks wires an agent's own hooks to `tspace signal`, so that status and\n" +
			"dash show which agents are working, blocked or done. Each subcommand\n" +
			"knows one agent's settings file. Nothing else in tspace depends on it:\n" +
			"any agent with hooks of its own can run `tspace signal --quiet STATE`,\n" +
			"and one without can write .context/signals/WINDOW.json itself; see\n" +
			"`tspace signal --help`.",
	}
	cmd.AddCommand(a.newConfigHooksClaudeCmd())
	return cmd
}

func (a *App) newConfigHooksClaudeCmd() *cobra.Command {
	var settings string
	var remove bool
	cmd := &cobra.Command{
		Use:   "claude",
		Short: "Install the signal hooks into Claude Code's user settings",
		Long: "claude adds three hooks to Claude Code's user settings,\n" +
			"~/.claude/settings.json (under $CLAUDE_CONFIG_DIR if that is set):\n\n" +
			"  UserPromptSubmit  tspace signal --quiet working\n" +
			"  Notification      tspace signal --quiet blocked\n" +
			"  Stop              tspace signal --quiet done\n\n" +
			"They are user-wide rather than in each worktree, where the settings file\n" +
			"would be an untracked file that clean refuses to remove. So they run in\n" +
			"every Claude Code session; --quiet makes them do nothing outside a task.\n\n" +
			"Everything else in the file is kept as it is, other hooks included. A\n" +
			"hook that already runs `tspace signal` is replaced, so running this again\n" +
			"changes nothing. --remove takes the tspace hooks out again.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path := settings
			if path == "" {
				p, err := claudeSettingsPath()
				if err != nil {
					return err
				}
				path = p
			}
			changed, err := updateClaudeHooks(path, !remove)
			if err != nil {
				return err
			}
			switch {
			case !changed && remove:
				a.printf("no tspace hooks in %s\n", path)
			case !changed:
				a.printf("the tspace hooks are already in %s\n", path)
			case remove:
				a.printf("removed the tspace hooks from %s\n", path)
			default:
				a.printf("installed the tspace hooks in %s:\n", path)
				for _, h := range claudeSignalHooks {
					a.printf("  %-17s %s\n", h.event, h.command)
				}
				a.printf("sessions started from now on use them\n")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&settings, "settings", "", "settings file to change (default: Claude Code's user settings)")
	cmd.Flags().BoolVar(&remove, "remove", false, "remove the tspace hooks instead")
	return cmd
}

// claudeSettingsPath is Claude Code's user settings file.
func claudeSettingsPath() (string, error) {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, "settings.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "settings.json"), nil
}

// updateClaudeHooks installs (or with install false, removes) the signal
// hooks in a settings file, and reports whether the file changed.
func updateClaudeHooks(path string, install bool) (bool, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		if !install {
			return false, nil
		}
		b = []byte("{}")
	} else if err != nil {
		return false, err
	}
	if len(bytes.TrimSpace(b)) == 0 {
		b = []byte("{}")
	}
	root, err := parseObject(b)
	if err != nil {
		return false, fmt.Errorf("%s: %w; fix it or move it aside, then run this again", path, err)
	}

	hooks := &object{}
	if raw, ok := root.get("hooks"); ok {
		if hooks, err = parseObject(raw); err != nil {
			return false, fmt.Errorf("%s: hooks: %w", path, err)
		}
	}
	for _, h := range claudeSignalHooks {
		var groups []json.RawMessage
		if raw, ok := hooks.get(h.event); ok {
			if err := json.Unmarshal(raw, &groups); err != nil {
				return false, fmt.Errorf("%s: hooks.%s is not a list: %w", path, h.event, err)
			}
		}
		groups, err := withoutSignalHooks(groups)
		if err != nil {
			return false, fmt.Errorf("%s: hooks.%s: %w", path, h.event, err)
		}
		if install {
			group, _ := json.Marshal(claudeHookGroup{Hooks: []claudeHook{{Type: "command", Command: h.command}}})
			groups = append(groups, group)
		}
		if len(groups) == 0 {
			hooks.del(h.event)
			continue
		}
		raw, err := json.Marshal(groups)
		if err != nil {
			return false, err
		}
		hooks.set(h.event, raw)
	}
	if len(hooks.keys) == 0 {
		root.del("hooks")
	} else {
		raw, err := hooks.marshal()
		if err != nil {
			return false, err
		}
		root.set("hooks", raw)
	}

	out, err := root.marshal()
	if err != nil {
		return false, err
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, out, "", "  "); err != nil {
		return false, err
	}
	pretty.WriteByte('\n')
	if same, err := sameJSON(b, pretty.Bytes()); err == nil && same {
		return false, nil
	}
	return true, writeFileAtomic(path, pretty.Bytes())
}

// withoutSignalHooks drops tspace signal hooks from a list of hook groups,
// and groups left with no hooks. Groups without one are kept byte for byte.
func withoutSignalHooks(groups []json.RawMessage) ([]json.RawMessage, error) {
	var out []json.RawMessage
	for _, raw := range groups {
		var g map[string]json.RawMessage
		if err := json.Unmarshal(raw, &g); err != nil {
			return nil, fmt.Errorf("a hook group is not an object: %w", err)
		}
		var list []map[string]any
		if h, ok := g["hooks"]; ok {
			if err := json.Unmarshal(h, &list); err != nil {
				return nil, fmt.Errorf("a group's hooks are not a list of objects: %w", err)
			}
		}
		var kept []map[string]any
		for _, h := range list {
			if c, _ := h["command"].(string); signalHookRe.MatchString(c) {
				continue
			}
			kept = append(kept, h)
		}
		switch {
		case len(kept) == len(list):
			out = append(out, raw)
		case len(kept) > 0:
			b, err := json.Marshal(kept)
			if err != nil {
				return nil, err
			}
			g["hooks"] = b
			b, err = json.Marshal(g)
			if err != nil {
				return nil, err
			}
			out = append(out, b)
		}
	}
	return out, nil
}

// sameJSON reports whether two documents hold the same value, so that an
// install with nothing to do leaves the file, and its formatting, untouched.
func sameJSON(a, b []byte) (bool, error) {
	var x, y any
	if err := json.Unmarshal(a, &x); err != nil {
		return false, err
	}
	if err := json.Unmarshal(b, &y); err != nil {
		return false, err
	}
	xb, _ := json.Marshal(x)
	yb, _ := json.Marshal(y)
	return bytes.Equal(xb, yb), nil
}

// object is a JSON object that keeps its keys in order, so a settings file a
// person arranged is written back the way they arranged it.
type object struct {
	keys []string
	vals map[string]json.RawMessage
}

func parseObject(b []byte) (*object, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, fmt.Errorf("not a JSON object")
	}
	o := &object{vals: map[string]json.RawMessage{}}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key := tok.(string)
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		o.set(key, v)
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, fmt.Errorf("unexpected data after the object")
	}
	return o, nil
}

func (o *object) get(key string) (json.RawMessage, bool) {
	v, ok := o.vals[key]
	return v, ok
}

func (o *object) set(key string, v json.RawMessage) {
	if o.vals == nil {
		o.vals = map[string]json.RawMessage{}
	}
	if _, ok := o.vals[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.vals[key] = v
}

func (o *object) del(key string) {
	if _, ok := o.vals[key]; !ok {
		return
	}
	delete(o.vals, key)
	for i, k := range o.keys {
		if k == key {
			o.keys = append(o.keys[:i], o.keys[i+1:]...)
			break
		}
	}
}

func (o *object) marshal() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		b.Write(kb)
		b.WriteByte(':')
		b.Write(o.vals[k])
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// writeFileAtomic replaces path through a temporary file beside it, keeping
// the old file's permissions, so a crash never leaves half a settings file.
func writeFileAtomic(path string, data []byte) error {
	mode := fs.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
