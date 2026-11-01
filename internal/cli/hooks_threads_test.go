package cli

import (
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/wuyuz99/taskspace/internal/task"
)

// The JSON Claude Code gives a hook, as far as tspace reads it. Notification
// never carries agent_id, even for a subagent's prompt.
const (
	mainThread = `{"tool_name":"Bash"}`
	subagent   = `{"tool_name":"Bash","agent_id":"a1"}`
	otherAgent = `{"tool_name":"Bash","agent_id":"a2"}`
	noThread   = `{"notification_type":"permission_prompt"}`
)

// hookStep is one hook firing and the state the window should then be in.
type hookStep struct {
	event, input string
	want         task.SignalState
}

// replay runs the commands an agent's installed hooks would, with the input
// each hook is given.
func replay(t *testing.T, agent hookAgent, h *harness, name string, steps []hookStep) {
	t.Helper()
	for i, s := range steps {
		ran := false
		for _, hook := range agent.hooks {
			if hook.event != s.event {
				continue
			}
			ran = true
			cmd := h.app.newSignalCmd()
			cmd.SetIn(strings.NewReader(s.input))
			if err := h.exec(t, cmd, strings.Fields(hook.command)[2:]...); err != nil {
				t.Fatal(err)
			}
		}
		if !ran {
			t.Fatalf("step %d: %s has no hook", i, s.event)
		}
		if got := h.signals(t, name)[task.RootWindowName].State; got != s.want {
			t.Fatalf("step %d: after %s %s: %s, want %s", i, s.event, s.input, got, s.want)
		}
	}
}

func hookTask(t *testing.T) (*harness, string) {
	t.Helper()
	h := newHarness(t)
	tk := h.startedRootTask(t, "hooks")
	chdir(t, h.app.Store.TaskDir(tk.Name))
	return h, tk.Name
}

// The orders below are what real Claude Code 2.1.316 fired; see #130.
func TestClaudeHooksFollowEachThread(t *testing.T) {
	const (
		working = task.SignalWorking
		blocked = task.SignalBlocked
		done    = task.SignalDone
	)
	for name, steps := range map[string][]hookStep{
		"a prompt is cleared when its tool returns": {
			{"UserPromptSubmit", mainThread, working},
			{"PermissionRequest", mainThread, blocked},
			{"Notification", noThread, blocked},
			{"PostToolUse", mainThread, working},
			{"Stop", mainThread, done},
		},
		"approved before Notification": {
			{"UserPromptSubmit", mainThread, working},
			{"PermissionRequest", mainThread, blocked},
			{"PostToolUse", mainThread, working},
		},
		"a failing tool clears its prompt": {
			{"PermissionRequest", mainThread, blocked},
			{"PostToolUseFailure", mainThread, working},
		},
		"a subagent's tools don't hide the main thread's prompt": {
			{"UserPromptSubmit", mainThread, working},
			{"PermissionRequest", mainThread, blocked},
			{"Notification", noThread, blocked},
			{"PostToolUse", subagent, blocked},
			{"PostToolUseFailure", subagent, blocked},
			{"PostToolUse", mainThread, working},
		},
		"a subagent outlives the turn, then asks": {
			{"UserPromptSubmit", mainThread, working},
			{"PostToolUse", mainThread, working},
			{"Stop", mainThread, done},
			{"PostToolUse", subagent, done},
			{"PermissionRequest", subagent, blocked},
			{"Notification", noThread, blocked},
			{"PostToolUse", subagent, working},
			{"UserPromptSubmit", mainThread, working},
			{"Stop", mainThread, done},
		},
		"Notification doesn't take a subagent's prompt for the main thread's": {
			{"PermissionRequest", subagent, blocked},
			{"Notification", noThread, blocked},
			{"PostToolUse", mainThread, blocked},
			{"PostToolUse", subagent, working},
		},
		"a question only Notification reports": {
			{"Notification", noThread, blocked},
			{"PostToolUse", subagent, blocked},
			{"PostToolUse", mainThread, working},
		},
		"two prompts answered in order": {
			{"PermissionRequest", mainThread, blocked},
			{"PermissionRequest", subagent, blocked},
			{"PostToolUse", mainThread, blocked},
			{"PostToolUse", subagent, working},
		},
		"another subagent's tools leave a subagent's prompt": {
			{"PermissionRequest", subagent, blocked},
			{"PostToolUse", otherAgent, blocked},
		},
		"a denied prompt doesn't outlast the turn": {
			{"PermissionRequest", mainThread, blocked},
			{"Stop", mainThread, done},
		},
		"a denial fires nothing, and the next prompt moves on": {
			{"PermissionRequest", mainThread, blocked},
			{"Notification", noThread, blocked},
			{"UserPromptSubmit", mainThread, working},
		},
	} {
		t.Run(name, func(t *testing.T) {
			h, task := hookTask(t)
			replay(t, claudeAgent, h, task, steps)
		})
	}
}

func TestGeminiHooksClearOnlyTheirOwnPrompt(t *testing.T) {
	h, name := hookTask(t)
	replay(t, geminiAgent, h, name, []hookStep{
		{"BeforeAgent", "{}", task.SignalWorking},
		{"Notification", "{}", task.SignalBlocked},
		{"AfterTool", "{}", task.SignalWorking},
		{"AfterAgent", "{}", task.SignalDone},
		{"AfterTool", "{}", task.SignalDone},
	})
}

// What an agent says for itself runs through its shell tool, so the tool's
// own hook comes straight after it and must not undo it.
func TestToolHooksKeepWhatTheAgentSaid(t *testing.T) {
	for _, tc := range []struct {
		state   task.SignalState
		message string
	}{
		{task.SignalBlocked, "round per line or per order?"},
		{task.SignalBlocked, ""},
		{task.SignalFailed, "gave up"},
		{task.SignalDone, ""},
	} {
		h, name := hookTask(t)
		if err := h.exec(t, h.app.newSignalCmd(), string(tc.state), tc.message); err != nil {
			t.Fatal(err)
		}
		replay(t, claudeAgent, h, name, []hookStep{{"PostToolUse", mainThread, tc.state}})
		if got := h.signals(t, name)[task.RootWindowName].Message; got != tc.message {
			t.Fatalf("message = %q, want %q", got, tc.message)
		}
		// Notification comes for a prompt the agent's own blocked may already
		// describe better.
		if tc.state == task.SignalBlocked {
			replay(t, claudeAgent, h, name, []hookStep{{"Notification", noThread, task.SignalBlocked}})
			if got := h.signals(t, name)[task.RootWindowName].Message; got != tc.message {
				t.Fatalf("after Notification, message = %q, want %q", got, tc.message)
			}
		}
	}
}

// The end of a turn is when an agent's own question has to stay on show.
func TestTurnEndKeepsWhatTheAgentSaid(t *testing.T) {
	for _, agent := range []struct {
		agent       hookAgent
		start, stop string
	}{
		{claudeAgent, "UserPromptSubmit", "Stop"},
		{geminiAgent, "BeforeAgent", "AfterAgent"},
	} {
		for _, state := range []task.SignalState{task.SignalBlocked, task.SignalFailed} {
			h, name := hookTask(t)
			if err := h.exec(t, h.app.newSignalCmd(), string(state), "which column?"); err != nil {
				t.Fatal(err)
			}
			replay(t, agent.agent, h, name, []hookStep{{agent.stop, "{}", state}})
			if got := h.signals(t, name)[task.RootWindowName].Message; got != "which column?" {
				t.Fatalf("message = %q", got)
			}
			// Answering it moves the window on.
			replay(t, agent.agent, h, name, []hookStep{
				{agent.start, "{}", task.SignalWorking},
				{agent.stop, "{}", task.SignalDone},
			})
		}
	}
}

// A signal's age is its file's, and working's means since the turn began.
func TestToolHooksKeepWorkingsAge(t *testing.T) {
	h, name := hookTask(t)
	replay(t, claudeAgent, h, name, []hookStep{{"UserPromptSubmit", mainThread, task.SignalWorking}})
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	path := h.app.Store.SignalPath(name, task.RootWindowName)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	replay(t, claudeAgent, h, name, []hookStep{{"PostToolUse", mainThread, task.SignalWorking}})
	if at := h.signals(t, name)[task.RootWindowName].At; !at.Equal(old) {
		t.Fatalf("At = %v, want %v", at, old)
	}
}

func TestSignalHookTreatsBadInputAsTheMainThread(t *testing.T) {
	for _, input := range []string{"", "not json", `{"agent_id":7}`} {
		if got := hookOwner(strings.NewReader(input)); got != task.SignalOwnerMain {
			t.Errorf("hookOwner(%q) = %q", input, got)
		}
	}
}

// An agent that leaves stdin open must not hang every tool on the hook.
func TestSignalHookDoesNotWaitForeverForInput(t *testing.T) {
	old := hookInputWait
	hookInputWait = 20 * time.Millisecond
	t.Cleanup(func() { hookInputWait = old })
	r, w := io.Pipe()
	defer w.Close()
	if got := hookOwner(r); got != task.SignalOwnerMain {
		t.Fatalf("hookOwner = %q", got)
	}
}

func TestSignalIfBlockedAfterStartIsANoOp(t *testing.T) {
	h, name := hookTask(t)
	cmd := h.app.newSignalCmd()
	cmd.SetIn(strings.NewReader(mainThread))
	if err := h.exec(t, cmd, "--hook", "--if-blocked", "working"); err != nil {
		t.Fatal(err)
	}
	if got := h.signals(t, name); len(got) != 0 {
		t.Fatalf("signals = %v", got)
	}
	if !strings.Contains(h.out.String(), "unchanged") {
		t.Fatalf("output = %q", h.out.String())
	}
}

func TestSignalRefusesConditionsThatMakeNoSense(t *testing.T) {
	h, _ := hookTask(t)
	for _, args := range [][]string{
		{"--if-blocked", "working"},
		{"--hook", "--if-blocked", "--unless-blocked", "working"},
		{"--clear", "--unless-blocked"},
		{"--clear", "--keep-reported"},
	} {
		cmd := h.app.newSignalCmd()
		cmd.SetIn(strings.NewReader("{}"))
		if err := h.exec(t, cmd, args...); err == nil {
			t.Errorf("%v succeeded", args)
		}
	}
}

// The owner is tspace's bookkeeping between hooks, not part of status --json.
func TestSignalOwnerStaysOutOfJSON(t *testing.T) {
	b, err := json.Marshal(task.Signal{State: task.SignalBlocked, Owner: "a1"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "a1") {
		t.Fatalf("%s", b)
	}
}
