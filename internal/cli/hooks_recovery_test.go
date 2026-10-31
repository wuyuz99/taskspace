package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wuyuz99/taskspace/internal/task"
)

func TestHooksUpgradeAndRecoverAfterPermission(t *testing.T) {
	for _, tc := range []struct {
		agent  hookAgent
		resume []string
		stop   string
	}{
		{claudeAgent, []string{"PostToolUse", "PostToolUseFailure"}, "Stop"},
		{geminiAgent, []string{"AfterTool"}, "AfterAgent"},
	} {
		t.Run(tc.agent.name, func(t *testing.T) {
			h := newHarness(t)
			tk := h.startedRootTask(t, "recover")
			chdir(t, h.app.Store.TaskDir(tk.Name))
			path := filepath.Join(t.TempDir(), "settings.json")
			// An existing installation, with user hooks on the newly managed
			// events: upgrading must preserve them, and removal must too.
			hooks := map[string]any{}
			for _, event := range tc.resume {
				hooks[event] = []hookGroup{{Hooks: []hookCommand{{Type: "command", Command: "audit"}}}}
			}
			hooks["Notification"] = []hookGroup{{Hooks: []hookCommand{{Type: "command", Command: "tspace signal --quiet blocked"}}}}
			b, err := json.Marshal(map[string]any{"hooks": hooks})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, b, 0o600); err != nil {
				t.Fatal(err)
			}
			install := func(remove bool) {
				t.Helper()
				args := []string{"hooks", tc.agent.name, "--settings", path}
				if remove {
					args = append(args, "--remove")
				}
				if err := h.exec(t, h.app.newConfigCmd(), args...); err != nil {
					t.Fatal(err)
				}
			}
			install(false)
			before, _ := os.ReadFile(path)
			install(false)
			if after, _ := os.ReadFile(path); string(after) != string(before) {
				t.Fatal("upgrade is not idempotent")
			}
			settings := readSettings(t, path)
			fire := func(event string, want task.SignalState) {
				t.Helper()
				for _, command := range commands(settings, event) {
					if command == "audit" {
						continue
					}
					args := strings.Fields(command)
					if err := h.exec(t, h.app.newSignalCmd(), args[2:]...); err != nil {
						t.Fatal(err)
					}
				}
				if got := h.signals(t, tk.Name)[task.RootWindowName].State; got != want {
					t.Fatalf("after %s: %s, want %s", event, got, want)
				}
			}
			for _, event := range tc.resume {
				if got := strings.Join(commands(settings, event), ","); got != "audit,tspace signal --quiet working" {
					t.Fatalf("%s: %s", event, got)
				}
				fire("Notification", task.SignalBlocked)
				fire(event, task.SignalWorking)
				fire(tc.stop, task.SignalDone)
			}
			install(true)
			for _, event := range tc.resume {
				if got := strings.Join(commands(readSettings(t, path), event), ","); got != "audit" {
					t.Fatalf("remove %s: %s", event, got)
				}
			}
		})
	}
}
