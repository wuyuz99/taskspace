package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readSettings(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("%v\n%s", err, b)
	}
	return m
}

// commands lists every hook command for an event, in order.
func commands(settings map[string]any, event string) []string {
	var out []string
	hooks, _ := settings["hooks"].(map[string]any)
	groups, _ := hooks[event].([]any)
	for _, g := range groups {
		list, _ := g.(map[string]any)["hooks"].([]any)
		for _, h := range list {
			c, _ := h.(map[string]any)["command"].(string)
			out = append(out, c)
		}
	}
	return out
}

func TestConfigHooksClaudeKeepsTheRestOfTheFile(t *testing.T) {
	h := newHarness(t)
	path := filepath.Join(t.TempDir(), "settings.json")
	original := `{
  "model": "opus",
  "hooks": {
    "Stop": [
      {"matcher": "", "hooks": [{"type": "command", "command": "say done"}]},
      {"hooks": [{"type": "command", "command": "tspace signal done"}]}
    ],
    "PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "audit"}]}]
  },
  "tui": "fullscreen"
}
`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newConfigCmd(), "hooks", "claude", "--settings", path); err != nil {
		t.Fatal(err)
	}
	got := readSettings(t, path)
	if got["model"] != "opus" || got["tui"] != "fullscreen" {
		t.Fatalf("other settings lost: %v", got)
	}
	if c := commands(got, "PreToolUse"); len(c) != 1 || c[0] != "audit" {
		t.Fatalf("PreToolUse = %v", c)
	}
	// The hand-written signal hook is replaced, the other Stop hook kept.
	if c := commands(got, "Stop"); strings.Join(c, ",") != "say done,tspace signal --quiet done" {
		t.Fatalf("Stop = %v", c)
	}
	for event, want := range map[string]string{"UserPromptSubmit": "tspace signal --quiet working", "Notification": "tspace signal --quiet blocked"} {
		if c := commands(got, event); len(c) != 1 || c[0] != want {
			t.Fatalf("%s = %v", event, c)
		}
	}
	b, _ := os.ReadFile(path)
	if strings.Index(string(b), `"model"`) > strings.Index(string(b), `"hooks"`) || strings.Index(string(b), `"hooks"`) > strings.Index(string(b), `"tui"`) {
		t.Fatalf("key order changed:\n%s", b)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 && info.Mode().Perm() != 0o666 {
		// Windows reports 0666 for any writable file.
		t.Errorf("mode = %v", info.Mode().Perm())
	}

	// Again: nothing to do, and the file is left byte for byte.
	h.out.Reset()
	if err := h.exec(t, h.app.newConfigCmd(), "hooks", "claude", "--settings", path); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(path); string(after) != string(b) || !strings.Contains(h.out.String(), "already") {
		t.Fatalf("second install changed the file or said so:\n%s", h.out.String())
	}

	// --remove takes out only tspace's hooks.
	if err := h.exec(t, h.app.newConfigCmd(), "hooks", "claude", "--settings", path, "--remove"); err != nil {
		t.Fatal(err)
	}
	got = readSettings(t, path)
	if c := commands(got, "Stop"); strings.Join(c, ",") != "say done" {
		t.Fatalf("Stop after remove = %v", c)
	}
	for _, event := range []string{"UserPromptSubmit", "Notification"} {
		if _, ok := got["hooks"].(map[string]any)[event]; ok {
			t.Errorf("%s left behind as an empty event", event)
		}
	}
}

func TestConfigHooksClaudeCreatesTheFile(t *testing.T) {
	h := newHarness(t)
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(dir, "claude"))
	if err := h.exec(t, h.app.newConfigCmd(), "hooks", "claude"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "claude", "settings.json")
	if c := commands(readSettings(t, path), "Notification"); len(c) != 1 {
		t.Fatalf("Notification = %v", c)
	}
	if err := h.exec(t, h.app.newConfigCmd(), "hooks", "claude", "--remove"); err != nil {
		t.Fatal(err)
	}
	if got := readSettings(t, path); len(got) != 0 {
		t.Fatalf("after remove: %v", got)
	}
}

func TestConfigHooksClaudeRefusesABrokenFile(t *testing.T) {
	h := newHarness(t)
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"model": `), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newConfigCmd(), "hooks", "claude", "--settings", path); err == nil {
		t.Fatal("broken settings accepted")
	}
	if b, _ := os.ReadFile(path); string(b) != `{"model": ` {
		t.Fatalf("broken file rewritten: %s", b)
	}
}

// A hook runs in every session: outside a task --quiet succeeds silently,
// and inside one it reports without printing anything the agent would see.
func TestSignalQuiet(t *testing.T) {
	h := newHarness(t)
	chdir(t, t.TempDir())
	if err := h.exec(t, h.app.newSignalCmd(), "--quiet", "working"); err != nil {
		t.Fatalf("outside a task: %v", err)
	}
	tk := h.startedTask(t, "PAY-1")
	repo := tk.RepoNames()[0]
	chdir(t, tk.Repos[repo].Worktree)
	h.out.Reset()
	if err := h.exec(t, h.app.newSignalCmd(), "-q", "done"); err != nil {
		t.Fatal(err)
	}
	if h.out.Len() != 0 {
		t.Fatalf("printed %q", h.out.String())
	}
	if got := h.signals(t, "PAY-1")[repo]; got.State != "done" {
		t.Fatalf("signal: %+v", got)
	}
	// A wrong state is still an error: the hook itself is broken.
	if err := h.exec(t, h.app.newSignalCmd(), "-q", "sleeping"); err == nil {
		t.Fatal("bad state accepted")
	}
}

func TestConfigHooksClaudeReviewCases(t *testing.T) {
	h := newHarness(t)
	dir := t.TempDir()

	// A command that does more than signal is the user's; && and > stay as typed.
	path := filepath.Join(dir, "settings.json")
	mine := `{"hooks": {"Stop": [{"hooks": [{"type": "command", "command": "tspace signal done && make lint > /tmp/x"}]}]}}`
	if err := os.WriteFile(path, []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newConfigCmd(), "hooks", "claude", "--settings", path); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "tspace signal done && make lint > /tmp/x") || strings.Contains(string(b), `\u00`+"26") {
		t.Fatalf("user hook changed:\n%s", b)
	}
	if c := commands(readSettings(t, path), "Stop"); len(c) != 2 {
		t.Fatalf("Stop = %v", c)
	}
	// Notification asks only for the notifications that need a person.
	var s struct {
		Hooks map[string][]claudeHookGroup `json:"hooks"`
	}
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	if g := s.Hooks["Notification"]; len(g) != 1 || g[0].Matcher != "permission_prompt|elicitation_dialog" {
		t.Fatalf("Notification = %+v", g)
	}

	// Empty entries and a null hooks value are the user's, not ours.
	empty := filepath.Join(dir, "empty.json")
	if err := os.WriteFile(empty, []byte(`{"hooks": {"Stop": []}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	h.out.Reset()
	if err := h.exec(t, h.app.newConfigCmd(), "hooks", "claude", "--settings", empty, "--remove"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(empty); string(b) != `{"hooks": {"Stop": []}}` || !strings.Contains(h.out.String(), "no tspace hooks") {
		t.Fatalf("remove touched a file with none:\n%s\n%s", b, h.out.String())
	}
	null := filepath.Join(dir, "null.json")
	if err := os.WriteFile(null, []byte(`{"hooks": null}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newConfigCmd(), "hooks", "claude", "--settings", null); err != nil {
		t.Fatalf("hooks: null refused: %v", err)
	}
}

func TestConfigHooksClaudeWritesThroughASymlink(t *testing.T) {
	h := newHarness(t)
	dir := t.TempDir()
	target := filepath.Join(dir, "dotfiles-settings.json")
	if err := os.WriteFile(target, []byte(`{"model": "opus"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "settings.json")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	if err := h.exec(t, h.app.newConfigCmd(), "hooks", "claude", "--settings", link); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("link replaced: %v", err)
	}
	if c := commands(readSettings(t, target), "Stop"); len(c) != 1 {
		t.Fatalf("target Stop = %v", c)
	}
}

// --quiet hides only "not in a task": a broken hook must still be noticed.
func TestSignalQuietStillReportsMistakes(t *testing.T) {
	h := newHarness(t)
	h.startedTask(t, "PAY-1")
	chdir(t, t.TempDir())
	if err := h.exec(t, h.app.newSignalCmd(), "-q", "--task", "PAY-14", "working"); err == nil {
		t.Fatal("unknown task accepted under --quiet")
	}
	if err := h.exec(t, h.app.newSignalCmd(), "-q", "--task", "PAY-1", "--repo", "nope", "working"); err == nil {
		t.Fatal("unknown repo accepted under --quiet")
	}
}
