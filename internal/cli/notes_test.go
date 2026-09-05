package cli

import (
	"os"
	"path/filepath"
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
