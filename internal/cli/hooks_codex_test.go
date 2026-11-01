package cli

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/wuyuz99/taskspace/internal/task"
)

// Exercise the pasted recipe, so its permission and completion hooks cannot
// drift apart when the signal flags change. Codex sends both successful and
// nonzero shell exits through PostToolUse.
func TestCodexDocumentedHooks(t *testing.T) {
	b, err := os.ReadFile("../../docs/STATUS.md")
	if err != nil {
		t.Fatal(err)
	}
	_, section, ok := strings.Cut(string(b), "**Codex**")
	if !ok {
		t.Fatal("missing Codex recipe")
	}
	_, section, ok = strings.Cut(section, "```json\n")
	if !ok {
		t.Fatal("missing recipe JSON")
	}
	section, _, ok = strings.Cut(section, "```")
	if !ok {
		t.Fatal("unclosed recipe JSON")
	}
	var recipe struct {
		Hooks map[string][]hookGroup `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(section), &recipe); err != nil {
		t.Fatal(err)
	}
	if len(recipe.Hooks) != 4 {
		t.Fatalf("want four events, got %d", len(recipe.Hooks))
	}
	agent := hookAgent{name: "codex"}
	for event, groups := range recipe.Hooks {
		for _, group := range groups {
			for _, command := range group.Hooks {
				agent.hooks = append(agent.hooks, signalHook{event: event, command: command.Command})
			}
		}
	}
	h, name := hookTask(t)
	replay(t, agent, h, name, []hookStep{
		{"UserPromptSubmit", `{}`, task.SignalWorking},
		{"PermissionRequest", `{"tool_name":"Bash"}`, task.SignalBlocked},
		{"PostToolUse", `{"tool_name":"Bash","tool_response":"ok"}`, task.SignalWorking},
		{"PermissionRequest", `{"tool_name":"Bash"}`, task.SignalBlocked},
		{"PostToolUse", `{"tool_name":"Bash","tool_response":"exit 7"}`, task.SignalWorking},
		{"Stop", `{}`, task.SignalDone},
		{"PostToolUse", `{"tool_name":"Bash"}`, task.SignalDone},
	})
	for _, state := range []task.SignalState{task.SignalWorking, task.SignalBlocked, task.SignalFailed} {
		if err := h.app.Store.WriteSignal(name, task.RootWindowName, state, "agent report"); err != nil {
			t.Fatal(err)
		}
		before := h.signals(t, name)[task.RootWindowName]
		replay(t, agent, h, name, []hookStep{{"PostToolUse", `{}`, state}})
		if after := h.signals(t, name)[task.RootWindowName]; after != before {
			t.Fatalf("tool completion changed an agent signal: %+v -> %+v", before, after)
		}
	}
}
