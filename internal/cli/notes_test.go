package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
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
	if tk.TicketURL != "" || tk.Brief != brief || tk.HandoffUpdatedAt.IsZero() {
		t.Fatalf("unexpected task: %+v", tk)
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
	tk, err = h.app.Store.Load("PAY-142")
	if err != nil {
		t.Fatal(err)
	}
	if tk.Handoff != "" || tk.Brief != brief {
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
	tk, err := h.app.Store.Load("PAY-142")
	if err != nil {
		t.Fatal(err)
	}
	if tk.Brief != brief {
		t.Fatalf("stored brief was rewritten: %q", tk.Brief)
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
	tk, err := h.app.Store.Load("PAY-142")
	if err != nil {
		t.Fatal(err)
	}
	if !tk.HandoffUpdatedAt.IsZero() {
		t.Fatalf("cleared handoff kept %v", tk.HandoffUpdatedAt)
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
	if tk.Handoff != "Run checkout tests next" || tk.Ticket != "PAY-142" {
		t.Fatal("clean lost context")
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
