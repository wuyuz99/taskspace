// Package agents holds tspace's integrations with particular agents. Each is
// opt-in and named after its agent, and the rest of tspace knows only what it
// implements: hooks that call tspace signal, or a
// workspace.ConversationCarrier.
package agents

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"

	"github.com/wuyuz99/taskspace/internal/jsonedit"
	"github.com/wuyuz99/taskspace/internal/task"
)

// SignalHook is one of an agent's hook events tspace listens to, and the
// signal it sends; note says, for the help, when the event fires if its
// name does not.
type SignalHook struct{ Event, Matcher, Command, Note string }

// HookAgent is an agent whose settings file `config hooks` can write. Each
// is opt-in and named after its agent; all of them only call `tspace signal`.
// The agents here keep hooks in the same shape, a JSON settings file with
// a "hooks" object of event lists of matcher groups.
type HookAgent struct {
	Name, Product string
	// Hooks are written in this order.
	Hooks []SignalHook
	// Settings is the agent's user settings file, and SettingsHelp how
	// the help names it.
	Settings     func() (string, error)
	SettingsHelp string
	// Notes end the help, after what every agent shares.
	Notes string
}

// Claude is Claude Code.
//
// Notification is limited by its matcher to the notifications that need a
// person. Claude Code also notifies when a finished agent has sat idle for a
// while, and that one must not turn "done" into "blocked".
//
// PermissionRequest fires at once and says which thread is asking, where
// Notification comes six seconds later and never does, so Notification only
// adds what PermissionRequest doesn't cover. Tool hooks fire for subagents
// too, so one clears only the blocked of its own thread: a background
// subagent's tools must not hide a prompt the main thread still has open.
var Claude = HookAgent{
	Name: "claude", Product: "Claude Code",
	Hooks: []SignalHook{
		{"UserPromptSubmit", "", "tspace signal --quiet working", ""},
		{"PermissionRequest", "", "tspace signal --quiet --hook blocked", "a tool needs approval"},
		{"Notification", "permission_prompt|elicitation_dialog", "tspace signal --quiet --hook --unless-blocked blocked", "permission prompts and questions"},
		{"PostToolUse", "", "tspace signal --quiet --hook --if-blocked working", "a tool completed after any approval"},
		{"PostToolUseFailure", "", "tspace signal --quiet --hook --if-blocked working", "a tool failed and the agent continues"},
		{"Stop", "", "tspace signal --quiet --keep-reported done", ""},
	},
	Settings:     claudeSettingsPath,
	SettingsHelp: "~/.claude/settings.json (under $CLAUDE_CONFIG_DIR if that is set)",
}

// Gemini is Gemini CLI. Its Notification ignores matchers and today
// fires only for tool permission prompts, so it needs none; BeforeAgent runs
// when a prompt is submitted, and AfterAgent once the turn's answer is done.
var Gemini = HookAgent{
	Name: "gemini", Product: "Gemini CLI",
	Hooks: []SignalHook{
		{"BeforeAgent", "", "tspace signal --quiet working", "a prompt was submitted"},
		{"Notification", "", "tspace signal --quiet --hook blocked", "tool permission prompts"},
		{"AfterTool", "", "tspace signal --quiet --hook --if-blocked working", "a tool completed after any approval"},
		{"AfterAgent", "", "tspace signal --quiet --keep-reported done", "the turn's answer is done"},
	},
	Settings:     geminiSettingsPath,
	SettingsHelp: "~/.gemini/settings.json (in $GEMINI_CLI_HOME instead of ~ if that is set)",
	Notes: "Hooks are on by default; hooksConfig.enabled false in the same file turns\n" +
		"them all off. Gemini CLI reads settings with comments in them, which tspace\n" +
		"cannot rewrite safely, so it refuses such a file.",
}

// HookGroup is one entry of an event's list, with its fields in the order
// the agents' own documentation shows them.
type HookGroup struct {
	Matcher string        `json:"matcher,omitempty"`
	Hooks   []HookCommand `json:"hooks"`
}

// HookCommand is one hook of a group.
type HookCommand struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

// signalHookRe recognises a hook that is a tspace signal call and nothing
// else, whatever path tspace is called by: one tspace installed, or one
// written by hand from the README. A command that does more, such as
// `tspace signal done && say done`, is the user's and is left alone.
var signalHookRe = regexp.MustCompile(`^\s*(\S*[/\\])?tspace(\.exe)?\s+signal(\s+[^\s;&|<>$` + "`" + `()]+)*\s*$`)

// claudeSettingsPath is Claude Code's user settings file.
func claudeSettingsPath() (string, error) {
	dir, err := ClaudeConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "settings.json"), nil
}

// ClaudeConfigDir is where Claude Code keeps its user files.
func ClaudeConfigDir() (string, error) {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude"), nil
}

// geminiSettingsPath is Gemini CLI's user settings file.
func geminiSettingsPath() (string, error) {
	home := os.Getenv("GEMINI_CLI_HOME")
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		home = h
	}
	return filepath.Join(home, ".gemini", "settings.json"), nil
}

// UpdateSignalHooks installs (or with install false, removes) signal hooks
// in a settings file, and reports whether the file changed. Only the event
// lists that gain or lose a tspace hook are rewritten; everything else is
// written back as the bytes it was read as.
func UpdateSignalHooks(path string, signalHooks []SignalHook, install bool) (bool, error) {
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
	root, err := jsonedit.Parse(b)
	if err != nil {
		return false, fmt.Errorf("%s: %w; fix it or move it aside, then run this again", path, err)
	}

	hooks := &jsonedit.Object{}
	if raw, ok := root.Get("hooks"); ok && !jsonedit.IsNull(raw) {
		if hooks, err = jsonedit.Parse(raw); err != nil {
			return false, fmt.Errorf("%s: hooks: %w", path, err)
		}
	}
	changed := false
	for _, h := range signalHooks {
		var groups []json.RawMessage
		if raw, ok := hooks.Get(h.Event); ok {
			if err := json.Unmarshal(raw, &groups); err != nil {
				return false, fmt.Errorf("%s: hooks.%s is not a list: %w", path, h.Event, err)
			}
		}
		kept, at, err := withoutSignalHooks(groups)
		if err != nil {
			return false, fmt.Errorf("%s: hooks.%s: %w", path, h.Event, err)
		}
		if !install && at < 0 {
			continue // nothing of ours here: leave the event exactly as it was
		}
		if install {
			group, err := jsonedit.MarshalPlain(HookGroup{Matcher: h.Matcher, Hooks: []HookCommand{{Type: "command", Command: h.Command}}})
			if err != nil {
				return false, err
			}
			// Where it was, so installing again changes nothing.
			if at < 0 {
				at = len(kept)
			}
			kept = slices.Insert(kept, at, group)
		}
		changed = true
		if len(kept) == 0 {
			hooks.Del(h.Event)
			continue
		}
		raw, err := jsonedit.MarshalList(kept)
		if err != nil {
			return false, err
		}
		hooks.Set(h.Event, raw)
	}
	if !changed {
		return false, nil
	}
	if hooks.Len() == 0 {
		root.Del("hooks")
	} else {
		root.Set("hooks", hooks.Marshal())
	}

	var pretty bytes.Buffer
	if err := json.Indent(&pretty, root.Marshal(), "", "  "); err != nil {
		return false, err
	}
	pretty.WriteByte('\n')
	if same, err := jsonedit.Same(b, pretty.Bytes()); err == nil && same {
		return false, nil
	}
	return true, task.ReplaceFile(path, pretty.Bytes())
}

// withoutSignalHooks drops tspace signal hooks from a list of hook groups,
// and groups left with no hooks, and reports where in what is left the first
// one was, or -1 when it dropped none. Groups and hooks it keeps are kept
// byte for byte, fields in their order.
func withoutSignalHooks(groups []json.RawMessage) ([]json.RawMessage, int, error) {
	var out []json.RawMessage
	at := -1
	for _, raw := range groups {
		g, err := jsonedit.Parse(raw)
		if err != nil {
			return nil, -1, fmt.Errorf("a hook group is not an object: %w", err)
		}
		var list []json.RawMessage
		if h, ok := g.Get("hooks"); ok {
			if err := json.Unmarshal(h, &list); err != nil {
				return nil, -1, fmt.Errorf("a group's hooks are not a list: %w", err)
			}
		}
		var kept []json.RawMessage
		// after is whether the group's first signal hook came after one of
		// the user's, so it goes back after the group, not before it.
		after, found := false, false
		for _, h := range list {
			var hook struct {
				Command string `json:"command"`
			}
			if json.Unmarshal(h, &hook) == nil && signalHookRe.MatchString(hook.Command) {
				if !found {
					after, found = len(kept) > 0, true
				}
				continue
			}
			kept = append(kept, h)
		}
		if found && at < 0 {
			at = len(out)
			if after {
				at++
			}
		}
		switch {
		case len(kept) == len(list):
			out = append(out, raw)
		case len(kept) > 0:
			l, err := jsonedit.MarshalList(kept)
			if err != nil {
				return nil, -1, err
			}
			g.Set("hooks", l)
			out = append(out, g.Marshal())
		}
	}
	return out, at, nil
}
