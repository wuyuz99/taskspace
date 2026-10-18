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
//
// Notification is limited by its matcher to the notifications that need a
// person. Claude Code also notifies when a finished agent has sat idle for a
// while, and that one must not turn "done" into "blocked".
var claudeSignalHooks = []struct{ event, matcher, command string }{
	{"UserPromptSubmit", "", "tspace signal --quiet working"},
	{"Notification", "permission_prompt|elicitation_dialog", "tspace signal --quiet blocked"},
	{"Stop", "", "tspace signal --quiet done"},
}

// claudeHookGroup is one entry of an event's list in Claude Code settings,
// with its fields in the order Claude Code's own documentation shows them.
type claudeHookGroup struct {
	Matcher string       `json:"matcher,omitempty"`
	Hooks   []claudeHook `json:"hooks"`
}

type claudeHook struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

// signalHookRe recognises a hook that is a tspace signal call and nothing
// else, whatever path tspace is called by: one tspace installed, or one
// written by hand from the README. A command that does more, such as
// `tspace signal done && say done`, is the user's and is left alone.
var signalHookRe = regexp.MustCompile(`^\s*(\S*[/\\])?tspace(\.exe)?\s+signal(\s+[^\s;&|<>$` + "`" + `()]+)*\s*$`)

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
			"  Notification      tspace signal --quiet blocked  (permission prompts and questions)\n" +
			"  Stop              tspace signal --quiet done\n\n" +
			"They are user-wide rather than in each worktree, where the settings file\n" +
			"would be an untracked file that clean refuses to remove. So they run in\n" +
			"every Claude Code session; --quiet makes them do nothing outside a task.\n\n" +
			"Everything else in the file is kept as it is, other hooks included. A\n" +
			"hook that is only a `tspace signal` call is replaced, so running this\n" +
			"again changes nothing. --remove takes the tspace hooks out again. A\n" +
			"settings file that is a symbolic link is written through the link.",
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
// hooks in a settings file, and reports whether the file changed. Only the
// event lists that gain or lose a tspace hook are rewritten; everything else
// is written back as the bytes it was read as.
func updateClaudeHooks(path string, install bool) (bool, error) {
	// A symbolic link, into a dotfiles repository say, is written through.
	if target, err := filepath.EvalSymlinks(path); err == nil {
		path = target
	}
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
	if raw, ok := root.get("hooks"); ok && !isNull(raw) {
		if hooks, err = parseObject(raw); err != nil {
			return false, fmt.Errorf("%s: hooks: %w", path, err)
		}
	}
	changed := false
	for _, h := range claudeSignalHooks {
		var groups []json.RawMessage
		if raw, ok := hooks.get(h.event); ok {
			if err := json.Unmarshal(raw, &groups); err != nil {
				return false, fmt.Errorf("%s: hooks.%s is not a list: %w", path, h.event, err)
			}
		}
		kept, removed, err := withoutSignalHooks(groups)
		if err != nil {
			return false, fmt.Errorf("%s: hooks.%s: %w", path, h.event, err)
		}
		if !install && !removed {
			continue // nothing of ours here: leave the event exactly as it was
		}
		if install {
			group, err := marshalPlain(claudeHookGroup{Matcher: h.matcher, Hooks: []claudeHook{{Type: "command", Command: h.command}}})
			if err != nil {
				return false, err
			}
			kept = append(kept, group)
		}
		changed = true
		if len(kept) == 0 {
			hooks.del(h.event)
			continue
		}
		raw, err := marshalList(kept)
		if err != nil {
			return false, err
		}
		hooks.set(h.event, raw)
	}
	if !changed {
		return false, nil
	}
	if len(hooks.keys) == 0 {
		root.del("hooks")
	} else {
		root.set("hooks", hooks.marshal())
	}

	var pretty bytes.Buffer
	if err := json.Indent(&pretty, root.marshal(), "", "  "); err != nil {
		return false, err
	}
	pretty.WriteByte('\n')
	if same, err := sameJSON(b, pretty.Bytes()); err == nil && same {
		return false, nil
	}
	return true, writeFileAtomic(path, pretty.Bytes())
}

// withoutSignalHooks drops tspace signal hooks from a list of hook groups,
// and groups left with no hooks, and reports whether it dropped any. Groups
// and hooks it keeps are kept byte for byte, fields in their order.
func withoutSignalHooks(groups []json.RawMessage) ([]json.RawMessage, bool, error) {
	var out []json.RawMessage
	removed := false
	for _, raw := range groups {
		g, err := parseObject(raw)
		if err != nil {
			return nil, false, fmt.Errorf("a hook group is not an object: %w", err)
		}
		var list []json.RawMessage
		if h, ok := g.get("hooks"); ok {
			if err := json.Unmarshal(h, &list); err != nil {
				return nil, false, fmt.Errorf("a group's hooks are not a list: %w", err)
			}
		}
		var kept []json.RawMessage
		for _, h := range list {
			var hook struct {
				Command string `json:"command"`
			}
			if json.Unmarshal(h, &hook) == nil && signalHookRe.MatchString(hook.Command) {
				continue
			}
			kept = append(kept, h)
		}
		switch {
		case len(kept) == len(list):
			out = append(out, raw)
		case len(kept) > 0:
			removed = true
			l, err := marshalList(kept)
			if err != nil {
				return nil, false, err
			}
			g.set("hooks", l)
			out = append(out, g.marshal())
		default:
			removed = true
		}
	}
	return out, removed, nil
}

// marshalPlain is json.Marshal without HTML escaping: a hook command such as
// `make lint && echo ok > log` must be written as typed, not as \u0026\u0026.
func marshalPlain(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}

// marshalList joins raw elements into a JSON array without re-encoding them.
func marshalList(items []json.RawMessage) ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('[')
	for i, it := range items {
		if i > 0 {
			b.WriteByte(',')
		}
		var compact bytes.Buffer
		if err := json.Compact(&compact, it); err != nil {
			return nil, err
		}
		b.Write(compact.Bytes())
	}
	b.WriteByte(']')
	return b.Bytes(), nil
}

func isNull(raw json.RawMessage) bool { return string(bytes.TrimSpace(raw)) == "null" }

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

// object is a JSON object that keeps its keys in order and its values as the
// bytes they were read as, so a settings file a person arranged is written
// back the way they arranged it.
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

// marshal writes the object with its values as stored; keys are plain
// strings, written with the same escaping-free encoder as everything else.
func (o *object) marshal() []byte {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, _ := marshalPlain(k)
		b.Write(kb)
		b.WriteByte(':')
		b.Write(o.vals[k])
	}
	b.WriteByte('}')
	return b.Bytes()
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
