package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/wuyuz99/taskspace/internal/task"
)

func TestTaskNotesWithoutTicketURL(t *testing.T) {
	h := newHarness(t)
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-142", "--title", "Handle expired cards"); err != nil {
		t.Fatal(err)
	}
	brief := "# Objective\nReject expired cards.\n\n## Acceptance criteria\n- Show an inline error.\n"
	file := filepath.Join(t.TempDir(), "brief.md")
	if err := os.WriteFile(file, []byte(brief), 0600); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newTaskCmd(), "brief", "PAY-142", "--file", file); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newTaskCmd(), "handoff", "PAY-142", "Backend done; frontend next"); err != nil {
		t.Fatal(err)
	}
	tk, err := h.app.Store.Load("PAY-142")
	if err != nil {
		t.Fatal(err)
	}
	storedBrief, err := h.app.Store.ReadNote("PAY-142", task.BriefFile)
	if err != nil {
		t.Fatal(err)
	}
	storedHandoff, err := h.app.Store.ReadNote("PAY-142", task.HandoffFile)
	if err != nil {
		t.Fatal(err)
	}
	if tk.TicketURL != "" || storedBrief.Text != brief || storedHandoff.UpdatedAt.IsZero() {
		t.Fatalf("unexpected task: %+v / %+v", tk, storedBrief)
	}
	if err := h.exec(t, h.app.newTaskCmd(), "brief", "PAY-142"); err != nil {
		t.Fatal(err)
	}
	if h.out.String() != brief {
		t.Fatalf("Markdown export changed: %q", h.out.String())
	}
	if err := h.exec(t, h.app.newTaskCmd(), "list"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"PAY-142", "Handle expired cards", "Backend done; frontend next", "UPDATED (UTC)"} {
		if !strings.Contains(h.out.String(), want) {
			t.Errorf("list missing %q: %s", want, h.out.String())
		}
	}
	if err := h.exec(t, h.app.newTaskCmd(), "handoff", "PAY-142", "--clear"); err != nil {
		t.Fatal(err)
	}
	clearedHandoff, err := h.app.Store.ReadNote("PAY-142", task.HandoffFile)
	if err != nil {
		t.Fatal(err)
	}
	keptBrief, err := h.app.Store.ReadNote("PAY-142", task.BriefFile)
	if err != nil {
		t.Fatal(err)
	}
	if clearedHandoff.Text != "" || keptBrief.Text != brief {
		t.Fatal("clearing handoff affected the brief or failed")
	}
}

// A brief comes from a file the user did not write by hand — a ticket export,
// most often — and it is printed on the way into attach, open and shell. An
// escape sequence in one must not reach the terminal.
func TestContextRenderingNeutralizesTerminalEscapes(t *testing.T) {
	h := newHarness(t)
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-142"); err != nil {
		t.Fatal(err)
	}
	brief := "Reject \x1b]2;pwned\x07expired cards\n"
	file := filepath.Join(t.TempDir(), "brief.md")
	if err := os.WriteFile(file, []byte(brief), 0600); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newTaskCmd(), "brief", "PAY-142", "--file", file); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newTaskCmd(), "handoff", "PAY-142", "next\x1b[2Jsteps"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newAttachCmd(), "PAY-142"); err != nil {
		t.Fatal(err)
	}
	shown := h.out.String()
	if strings.ContainsAny(shown, "\x1b\x07") {
		t.Fatalf("control characters reached the terminal: %q", shown)
	}
	if !strings.Contains(shown, "Reject ]2;pwnedexpired cards") {
		t.Fatalf("readable text was lost: %q", shown)
	}
	// Stripping is a rendering rule; the stored Markdown keeps every byte.
	stored, err := h.app.Store.ReadNote("PAY-142", task.BriefFile)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Text != brief {
		t.Fatalf("stored brief was rewritten: %q", stored.Text)
	}
}

// Printing the note is how it gets back out to a file or a pager, so it stays
// byte-for-byte — but it must not glue the shell prompt to the last line.
func TestNoteExportEndsWithANewline(t *testing.T) {
	h := newHarness(t)
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-142"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newTaskCmd(), "brief", "PAY-142", "no trailing newline"); err != nil {
		t.Fatal(err)
	}
	h.out.Reset()
	if err := h.exec(t, h.app.newTaskCmd(), "brief", "PAY-142"); err != nil {
		t.Fatal(err)
	}
	if h.out.String() != "no trailing newline\n" {
		t.Fatalf("export = %q", h.out.String())
	}
	// An empty note prints nothing at all, not a bare newline.
	h.out.Reset()
	if err := h.exec(t, h.app.newTaskCmd(), "handoff", "PAY-142"); err != nil {
		t.Fatal(err)
	}
	if h.out.String() != "" {
		t.Fatalf("empty note printed %q", h.out.String())
	}
}

// A cleared handoff has no summary, so it must not keep a timestamp beside the
// empty column.
func TestClearedHandoffDropsItsTimestamp(t *testing.T) {
	h := newHarness(t)
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-142"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newTaskCmd(), "handoff", "PAY-142", "Backend done"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newTaskCmd(), "handoff", "PAY-142", "--clear"); err != nil {
		t.Fatal(err)
	}
	note, err := h.app.Store.ReadNote("PAY-142", task.HandoffFile)
	if err != nil {
		t.Fatal(err)
	}
	if !note.UpdatedAt.IsZero() {
		t.Fatalf("cleared handoff kept %v", note.UpdatedAt)
	}
	h.out.Reset()
	if err := h.exec(t, h.app.newTaskCmd(), "list"); err != nil {
		t.Fatal(err)
	}
	if date := regexp.MustCompile(`\d{4}-\d{2}-\d{2}`); date.MatchString(h.out.String()) {
		t.Fatalf("list showed a timestamp for a cleared handoff: %s", h.out.String())
	}
}

// One corrupt record must not make the whole listing refuse to run.
func TestListsDegradeAroundUnreadableRecords(t *testing.T) {
	h := newHarness(t)
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-142"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.app.Store.TasksDir(), "broken.json"), []byte("{not json"), 0600); err != nil {
		t.Fatal(err)
	}
	h.out.Reset()
	h.errb.Reset()
	if err := h.exec(t, h.app.newTaskCmd(), "list"); err != nil {
		t.Fatalf("list refused to run: %v", err)
	}
	if !strings.Contains(h.out.String(), "PAY-142") {
		t.Fatalf("readable task was hidden: %s", h.out.String())
	}
	if !strings.Contains(h.errb.String(), "broken") {
		t.Fatalf("unreadable record was not reported: %s", h.errb.String())
	}
}

func TestTaskNotesSurviveCleanAndAppearOnAttach(t *testing.T) {
	h := newHarness(t)
	h.startedTask(t, "ticket-work")
	if err := h.exec(t, h.app.newTaskCmd(), "update", "ticket-work", "--ticket", "PAY-142", "--title", "Expired cards"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newTaskCmd(), "handoff", "ticket-work", "Run checkout tests next"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newAttachCmd(), "ticket-work"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.out.String(), "Run checkout tests next") {
		t.Fatal("attach did not show handoff")
	}
	if err := h.exec(t, h.app.newCleanCmd(), "ticket-work"); err != nil {
		t.Fatal(err)
	}
	tk, err := h.app.Store.Load("ticket-work")
	if err != nil {
		t.Fatal(err)
	}
	note, err := h.app.Store.ReadNote("ticket-work", task.HandoffFile)
	if err != nil {
		t.Fatal(err)
	}
	if note.Text != "Run checkout tests next" || tk.Ticket != "PAY-142" {
		t.Fatal("clean lost context")
	}
}

// A record written before notes were files is migrated the first time anything
// reads a note, with no flag and no separate command.
// A legacy record's notes are shown from the record by reads, which change
// nothing, and moved into files by the first command that changes the task.
func TestLegacyRecordsMigrateOnFirstChange(t *testing.T) {
	h := newHarness(t)
	stamp := time.Date(2026, 9, 1, 10, 30, 0, 0, time.UTC)
	legacy := `{
  "name": "PAY-142",
  "title": "Expired cards",
  "brief": "Reject expired cards.\n",
  "handoff": "Frontend next.\n",
  "handoff_updated_at": "2026-09-01T10:30:00Z"
}`
	if err := os.MkdirAll(h.app.Store.TasksDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.app.Store.TasksDir(), "PAY-142.json"), []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}

	h.out.Reset()
	if err := h.exec(t, h.app.newTaskCmd(), "brief", "PAY-142"); err != nil {
		t.Fatal(err)
	}
	if h.out.String() != "Reject expired cards.\n" {
		t.Fatalf("brief of a legacy record = %q", h.out.String())
	}
	if err := h.exec(t, h.app.newTaskCmd(), "show", "PAY-142"); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(filepath.Join(h.app.Store.TasksDir(), "PAY-142.json")); err != nil || string(raw) != legacy {
		t.Fatalf("reading rewrote the record: %s (%v)", raw, err)
	}

	if err := h.exec(t, h.app.newTaskCmd(), "update", "PAY-142", "--ticket", "PAY-142"); err != nil {
		t.Fatal(err)
	}
	brief, err := h.app.Store.ReadNote("PAY-142", task.BriefFile)
	if err != nil || brief.Text != "Reject expired cards.\n" {
		t.Fatalf("brief file = %q (%v)", brief.Text, err)
	}
	handoff, err := h.app.Store.ReadNote("PAY-142", task.HandoffFile)
	if err != nil {
		t.Fatal(err)
	}
	if !handoff.UpdatedAt.Equal(stamp) {
		t.Errorf("handoff time = %v, want the recorded %v", handoff.UpdatedAt, stamp)
	}
	raw, err := os.ReadFile(filepath.Join(h.app.Store.TasksDir(), "PAY-142.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "Reject expired cards") {
		t.Errorf("record still holds the brief: %s", raw)
	}
	if !strings.Contains(string(raw), "Expired cards") {
		t.Errorf("migration dropped the title: %s", raw)
	}
}

// Notes are deleted with the task record, as documented — but --force leaves
// the worktrees on disk, so it leaves the brief describing them too.
func TestTaskRmDeletesNotesUnlessWorktreesRemain(t *testing.T) {
	h := newHarness(t)
	if err := h.exec(t, h.app.newTaskCmd(), "create", "gone"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newTaskCmd(), "brief", "gone", "Requirements"); err != nil {
		t.Fatal(err)
	}
	h.out.Reset()
	if err := h.exec(t, h.app.newTaskCmd(), "rm", "gone"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.out.String(), task.BriefFile) {
		t.Errorf("rm did not say what it deleted: %s", h.out.String())
	}
	if _, err := os.Stat(h.app.Store.ContextDir("gone")); !os.IsNotExist(err) {
		t.Errorf("notes survived task rm: %v", err)
	}

	kept := h.startedTask(t, "kept")
	if err := h.exec(t, h.app.newTaskCmd(), "brief", kept.Name, "Requirements"); err != nil {
		t.Fatal(err)
	}
	h.errb.Reset()
	if err := h.exec(t, h.app.newTaskCmd(), "rm", kept.Name, "--force"); err != nil {
		t.Fatal(err)
	}
	note, err := h.app.Store.ReadNote(kept.Name, task.BriefFile)
	if err != nil || note.Text != "Requirements" {
		t.Fatalf("--force deleted the notes for worktrees still on disk: %q (%v)", note.Text, err)
	}
	if !strings.Contains(h.errb.String(), h.app.Store.ContextDir(kept.Name)) {
		t.Errorf("--force did not report where the notes are: %s", h.errb.String())
	}
}

// The worktrees are disposable; the brief describing them is not.
func TestCleanKeepsTheContextDirectory(t *testing.T) {
	h := newHarness(t)
	tk := h.startedTask(t, "ticket-work")
	if err := h.exec(t, h.app.newTaskCmd(), "brief", tk.Name, "Requirements"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newCleanCmd(), tk.Name); err != nil {
		t.Fatal(err)
	}
	note, err := h.app.Store.ReadNote(tk.Name, task.BriefFile)
	if err != nil || note.Text != "Requirements" {
		t.Fatalf("clean removed the notes: %q (%v)", note.Text, err)
	}
	for name := range tk.Repos {
		if _, err := os.Stat(tk.Repos[name].Worktree); !os.IsNotExist(err) {
			t.Errorf("clean left the %s worktree behind: %v", name, err)
		}
	}
}

func TestTaskDetailsValidationDoesNotCreateRecord(t *testing.T) {
	h := newHarness(t)
	for _, flags := range [][]string{{"--brief-file", filepath.Join(t.TempDir(), "missing.md")}, {"--ticket-url", "not-a-url"}} {
		args := append([]string{"create", "bad"}, flags...)
		if err := h.exec(t, h.app.newTaskCmd(), args...); err == nil {
			t.Fatal("expected invalid input to fail")
		}
		if h.app.Store.Exists("bad") {
			t.Fatal("invalid input created a task")
		}
	}
}

func TestMultipleWorkspacesCanReferenceSameTicket(t *testing.T) {
	h := newHarness(t)
	for _, name := range []string{"PAY-142", "PAY-142-alternative"} {
		if err := h.exec(t, h.app.newTaskCmd(), "create", name, "--ticket", "PAY-142"); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.exec(t, h.app.newTaskCmd(), "update", "PAY-142", "--title", "First approach"); err != nil {
		t.Fatal(err)
	}
	tk, err := h.app.Store.Load("PAY-142-alternative")
	if err != nil {
		t.Fatal(err)
	}
	if tk.Title != "" || tk.Ticket != "PAY-142" {
		t.Fatal("workspace identity was coupled to ticket")
	}
}

func TestLegacyRecordsAndCommandsThatDoNotSave(t *testing.T) {
	h := newHarness(t)
	if err := os.MkdirAll(h.app.Store.TasksDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := `{"name": "PAY-1", "brief": "old brief", "handoff": "old handoff"}`
	path := filepath.Join(h.app.Store.TasksDir(), "PAY-1.json")
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	// A file where the notes go: no upgrade can move them.
	if err := os.MkdirAll(h.app.Store.TaskDir("PAY-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(h.app.Store.ContextDir("PAY-1"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newStopCmd(), "PAY-1"); err != nil {
		t.Fatalf("stop of a record that cannot be upgraded: %v", err)
	}
	_ = os.Remove(h.app.Store.ContextDir("PAY-1")) // stop may have tidied it away
	if raw, _ := os.ReadFile(path); string(raw) != legacy {
		t.Fatalf("stop rewrote the record: %s", raw)
	}

	// Clearing a note of a legacy record is not undone by moving the other.
	if err := h.exec(t, h.app.newTaskCmd(), "handoff", "PAY-1", "--clear"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newTaskCmd(), "handoff", "PAY-1"); err != nil || h.out.String() != "" {
		t.Fatalf("handoff after --clear = %q, %v", h.out.String(), err)
	}
	if err := h.exec(t, h.app.newTaskCmd(), "brief", "PAY-1"); err != nil || !strings.Contains(h.out.String(), "old brief") {
		t.Fatalf("brief = %q, %v", h.out.String(), err)
	}
}
