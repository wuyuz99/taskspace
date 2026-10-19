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
	// GROUP help [COMMAND] still shows help.
	if err := h.exec(t, root, "task", "help", "create"); err != nil || !strings.Contains(h.out.String(), "create NAME") {
		t.Fatalf("task help create = %v, out %q", err, h.out.String())
	}
	// A mistyped config subcommand gets a suggestion too.
	err = h.exec(t, root, "config", "ticket-patern", "x")
	if err == nil || !strings.Contains(err.Error(), "ticket-pattern") {
		t.Fatalf("config ticket-patern = %v", err)
	}
	// config has its own RunE, which shows the config, and is left alone.
	if err := h.exec(t, root, "config"); err != nil || !strings.Contains(h.out.String(), "agent:") {
		t.Fatalf("config = %v, out %q", err, h.out.String())
	}
}

func TestCreateAliases(t *testing.T) {
	h := newHarness(t)
	if err := h.exec(t, h.app.newTaskCmd(), "new", "PAY-1"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, shortcut(h.app.newTaskCreateCmd(), "new"), "PAY-3"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"PAY-1", "PAY-3"} {
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
	// Patterns that match an empty name would find nothing in most names.
	if err := h.exec(t, h.app.newConfigCmd(), "ticket-pattern", "[0-9]*"); err == nil {
		t.Fatal("empty-matching pattern accepted")
	}
	// Plain parentheses are alternation; the ticket is still the whole match.
	if err := h.exec(t, h.app.newConfigCmd(), "ticket-pattern", "(ENG|OPS)-[0-9]+"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newTaskCmd(), "create", "OPS-3-x"); err != nil {
		t.Fatal(err)
	}
	if got := h.load(t, "OPS-3-x").Ticket; got != "OPS-3" {
		t.Errorf("ticket %q, want OPS-3", got)
	}
	// A group named ticket picks out part of the match.
	if err := h.exec(t, h.app.newConfigCmd(), "ticket-pattern", "^gh-(?P<ticket>[0-9]+)$"); err != nil {
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

	// A pattern broken by hand is reported, not silently ignored.
	c, err := h.app.Store.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	c.TicketPattern = "[A-Z+-"
	if err := h.app.Store.SaveConfig(c); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newTaskCmd(), "create", "ENG-10"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.errb.String(), "no ticket read from the name") {
		t.Errorf("stderr %q", h.errb.String())
	}
	if err := h.exec(t, h.app.newConfigCmd()); err != nil || !strings.Contains(h.out.String(), "not used:") {
		t.Errorf("config = %v, out %q", err, h.out.String())
	}
}
