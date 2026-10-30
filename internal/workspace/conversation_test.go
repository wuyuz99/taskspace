package workspace

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	tsgit "github.com/wuyuz99/taskspace/internal/git"
	"github.com/wuyuz99/taskspace/internal/task"
)

// fakeCarrier keeps history for the directories in has, and records the
// moves it is asked for.
type fakeCarrier struct {
	has   map[string]bool
	err   error
	calls [][2]string
}

func (c *fakeCarrier) Carry(from, to string) (bool, error) {
	c.calls = append(c.calls, [2]string{from, to})
	if c.err != nil {
		return false, c.err
	}
	if !c.has[from] {
		return false, nil
	}
	delete(c.has, from)
	c.has[to] = true
	return true, nil
}

// conversationFixture is a renameable task whose root agent and repository
// a's own agent have both run and resume, with c chosen to carry
// conversations when chosen is set.
func conversationFixture(t *testing.T, c *fakeCarrier, chosen bool) (*fixture, *task.Task) {
	t.Helper()
	f, tk := renameFixture(t)
	f.svc.Carriers = map[string]ConversationCarrier{"fake": c}
	if chosen {
		if err := f.svc.Store.SaveConfig(&task.Config{CarryConversations: []string{"fake"}}); err != nil {
			t.Fatal(err)
		}
	}
	tk.Resume = []string{"agent", "--continue"}
	r := tk.Repos["a"]
	r.Agent, r.Resume = []string{"repo-agent"}, []string{"repo-agent", "--resume"}
	tk.Repos["a"] = r
	if err := f.svc.Store.Save(tk); err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{task.RootWindowName, "a"} {
		if err := f.svc.Store.MarkLaunched("OLD", w); err != nil {
			t.Fatal(err)
		}
	}
	f.events = nil
	return f, tk
}

func TestRenameCarriesConversations(t *testing.T) {
	c := &fakeCarrier{has: map[string]bool{}}
	f, tk := conversationFixture(t, c, true)
	oldRoot, oldA := f.svc.Store.TaskDir("OLD"), tk.Repos["a"].Worktree
	c.has[oldRoot], c.has[oldA] = true, true
	if err := f.svc.Rename(t.Context(), "OLD", "NEW", RenameOptions{}); err != nil {
		t.Fatal(err)
	}
	newRoot, newA := f.svc.Store.TaskDir("NEW"), f.svc.Store.WorktreePath("NEW", "a")
	if !c.has[newRoot] || !c.has[newA] || len(c.has) != 2 {
		t.Fatalf("history now at %v", c.has)
	}
	// Repository b moved too, and is asked about though it has no history.
	if len(c.calls) != 3 {
		t.Fatalf("calls = %v", c.calls)
	}
	carried := eventsOf[ConversationCarried](f.events)
	if len(carried) != 2 || carried[0] != (ConversationCarried{Task: "NEW", Window: task.RootWindowName, Agent: "fake", From: oldRoot, To: newRoot}) ||
		carried[1].Window != "a" || carried[1].From != oldA || carried[1].To != newA {
		t.Fatalf("carried = %+v", carried)
	}
	if w := eventsOf[Warning](f.events); len(w) != 0 {
		t.Fatalf("warnings = %+v", w)
	}
}

// What a carrier did not carry, because there was none or it failed, is
// still named; one failure does not stop the rest.
func TestRenameNotesConversationsNotCarried(t *testing.T) {
	c := &fakeCarrier{has: map[string]bool{}}
	f, tk := conversationFixture(t, c, true)
	c.has[f.svc.Store.TaskDir("OLD")] = true
	oldA := tk.Repos["a"].Worktree
	if err := f.svc.Rename(t.Context(), "OLD", "NEW", RenameOptions{}); err != nil {
		t.Fatal(err)
	}
	w := eventsOf[Warning](f.events)
	if len(w) != 1 || !strings.Contains(w[0].Text, "NEW's a agent resumes by directory") || !strings.Contains(w[0].Text, oldA) {
		t.Fatalf("warnings = %+v", w)
	}

	c = &fakeCarrier{has: map[string]bool{}, err: errors.New("disk full")}
	f, _ = conversationFixture(t, c, true)
	if err := f.svc.Rename(t.Context(), "OLD", "NEW", RenameOptions{}); err != nil {
		t.Fatal(err)
	}
	w = eventsOf[Warning](f.events)
	var failed, notes int
	for _, x := range w {
		switch {
		case strings.Contains(x.Text, "could not carry") && strings.Contains(x.Text, "disk full"):
			failed++
		case strings.Contains(x.Text, "resumes by directory"):
			notes++
		}
	}
	if failed != 3 || notes != 2 || len(c.calls) != 3 {
		t.Fatalf("warnings = %+v; calls = %v", w, c.calls)
	}
}

// A carrier the config does not choose never runs, and a rename that rolls
// back carries nothing.
func TestRenameCarriesOnlyWhenChosenAndDone(t *testing.T) {
	c := &fakeCarrier{has: map[string]bool{}}
	f, _ := conversationFixture(t, c, false)
	if err := f.svc.Rename(t.Context(), "OLD", "NEW", RenameOptions{}); err != nil {
		t.Fatal(err)
	}
	if len(c.calls) != 0 || len(eventsOf[Warning](f.events)) != 2 {
		t.Fatalf("calls = %v; events = %+v", c.calls, f.events)
	}

	c = &fakeCarrier{has: map[string]bool{}}
	f, _ = conversationFixture(t, c, true)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	f.svc.Git = tsgit.New(&renameFailRunner{fail: 2, cancel: cancel})
	if err := f.svc.Rename(ctx, "OLD", "NEW", RenameOptions{}); err == nil {
		t.Fatal("rename succeeded")
	}
	if len(c.calls) != 0 {
		t.Fatalf("carried after a failed rename: %v", c.calls)
	}
}

// A config it cannot read costs a plain rename its carriers, not the rename.
func TestRenameWithUnreadableConfig(t *testing.T) {
	c := &fakeCarrier{has: map[string]bool{}}
	f, _ := conversationFixture(t, c, false)
	if err := os.WriteFile(f.svc.Store.ConfigPath(), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Rename(t.Context(), "OLD", "NEW", RenameOptions{}); err != nil {
		t.Fatal(err)
	}
	w := eventsOf[Warning](f.events)
	if len(c.calls) != 0 || len(w) != 3 || !strings.Contains(w[0].Text, "carrying no conversations") {
		t.Fatalf("calls = %v; warnings = %+v", c.calls, w)
	}
}
