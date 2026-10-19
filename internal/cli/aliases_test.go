package cli

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestMistypedSubcommandFailsWithASuggestion(t *testing.T) {
	h := newHarness(t)
	root := &cobra.Command{Use: "tspace"}
	root.AddCommand(h.app.newTaskCmd(), h.app.newConfigCmd())
	requireSubcommand(root)

	err := h.exec(t, root, "task", "crate", "PAY-1")
	if err == nil || !strings.Contains(err.Error(), "Did you mean this?\n\tcreate") {
		t.Fatalf("task crate = %v", err)
	}
	if h.app.Store.Exists("PAY-1") {
		t.Fatal("a mistyped command created the task")
	}
	// A group alone still shows its help.
	if err := h.exec(t, root, "task"); err != nil || !strings.Contains(h.out.String(), "Available Commands") {
		t.Fatalf("task = %v, out %q", err, h.out.String())
	}
	// config has its own RunE, which shows the config, and is left alone.
	if err := h.exec(t, root, "config"); err != nil || !strings.Contains(h.out.String(), "agent:") {
		t.Fatalf("config = %v, out %q", err, h.out.String())
	}
}

func TestCreateAliases(t *testing.T) {
	h := newHarness(t)
	for _, args := range [][]string{{"new", "PAY-1"}, {"add", "PAY-2"}} {
		if err := h.exec(t, h.app.newTaskCmd(), args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.exec(t, h.app.newShortcutCmd(h.app.newTaskCreateCmd(), "new"), "PAY-3"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"PAY-1", "PAY-2", "PAY-3"} {
		if !h.app.Store.Exists(name) {
			t.Errorf("%s not created", name)
		}
	}
}

func TestTicketPattern(t *testing.T) {
	h := newHarness(t)
	if err := h.exec(t, h.app.newConfigCmd(), "ticket-pattern", "("); err == nil {
		t.Fatal("invalid pattern accepted")
	}
	if err := h.exec(t, h.app.newConfigCmd(), "ticket-pattern", "[A-Z]+-[0-9]+"); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"create", "ENG-12-login"}, "ENG-12"},
		{[]string{"create", "misc"}, ""},
		{[]string{"create", "ENG-7", "--ticket", "OPS-1"}, "OPS-1"},
		{[]string{"create", "ENG-8", "--ticket", ""}, ""},
	}
	for _, c := range cases {
		if err := h.exec(t, h.app.newTaskCmd(), c.args...); err != nil {
			t.Fatal(err)
		}
		if got := h.load(t, c.args[1]).Ticket; got != c.want {
			t.Errorf("%v: ticket %q, want %q", c.args, got, c.want)
		}
	}
	// A capture group picks out part of the match.
	if err := h.exec(t, h.app.newConfigCmd(), "ticket-pattern", "^gh-([0-9]+)$"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newTaskCmd(), "create", "gh-42"); err != nil {
		t.Fatal(err)
	}
	if got := h.load(t, "gh-42").Ticket; got != "42" {
		t.Errorf("ticket %q, want 42", got)
	}
	if err := h.exec(t, h.app.newConfigCmd(), "ticket-pattern", "--unset"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newTaskCmd(), "create", "ENG-9"); err != nil {
		t.Fatal(err)
	}
	if got := h.load(t, "ENG-9").Ticket; got != "" {
		t.Errorf("unset pattern still found %q", got)
	}
}
