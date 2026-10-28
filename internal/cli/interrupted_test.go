package cli

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/wuyuz99/taskspace/internal/status"
)

// crashedTasks has A and B, both started and then lost with the multiplexer
// server, and C, started and stopped on purpose.
func crashedTasks(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	for _, name := range []string{"A", "B", "C"} {
		h.startedTask(t, name)
		if err := h.exec(t, h.app.newStartCmd(), name); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.exec(t, h.app.newStopCmd(), "C"); err != nil {
		t.Fatal(err)
	}
	// The machine restarts: no session survives.
	h.app.Runtime = &sessionStub{}
	return h
}

// sessionStub has a session once anything is started in it.
type sessionStub struct{ stubRuntime }

func (s *sessionStub) HasSession(context.Context, string) (bool, error) {
	return len(s.started) > 0, nil
}

func TestStatusShowsInterrupted(t *testing.T) {
	h := crashedTasks(t)
	h.out.Reset()
	if err := h.exec(t, h.app.newStatusCmd(), "--json"); err != nil {
		t.Fatal(err)
	}
	var got []statusJSON
	if err := json.Unmarshal(h.out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"A": true, "B": true, "C": false}
	for _, ts := range got {
		if ts.Interrupted != want[ts.Task] {
			t.Fatalf("%s interrupted = %v", ts.Task, ts.Interrupted)
		}
		if r := ts.Repos[0].Runtime; (r == string(status.RuntimeInterrupted)) != want[ts.Task] {
			t.Fatalf("%s runtime %s", ts.Task, r)
		}
	}
	// A task never started is no-session, not interrupted.
	h.startedTask(t, "D")
	ts := h.app.aggregator().Task(context.Background(), h.load(t, "D"))
	if ts.Interrupted || ts.Repos[0].Runtime != status.RuntimeNoSession {
		t.Fatalf("never started: %+v", ts)
	}
}

func TestStartInterrupted(t *testing.T) {
	h := crashedTasks(t)
	rt := h.app.Runtime.(*sessionStub)
	h.out.Reset()
	if err := h.exec(t, h.app.newStartCmd(), "--interrupted"); err != nil {
		t.Fatal(err)
	}
	out := h.out.String()
	if !strings.Contains(out, "== A") || !strings.Contains(out, "== B") || strings.Contains(out, "== C") || !strings.Contains(out, "started 2 interrupted tasks") {
		t.Fatalf("output:\n%s", out)
	}
	if len(rt.started) != 2 {
		t.Fatalf("started %+v", rt.started)
	}
	// Running again, nothing is interrupted: A and B run, C was stopped.
	h.out.Reset()
	if err := h.exec(t, h.app.newStartCmd(), "--interrupted"); err != nil || !strings.Contains(h.out.String(), "no task was interrupted") {
		t.Fatalf("again: %v\n%s", err, h.out.String())
	}
	if err := h.exec(t, h.app.newStartCmd(), "--interrupted", "A"); err == nil {
		t.Fatal("--interrupted with a TASK accepted")
	}
}

// One task that fails to start does not stop the rest.
func TestStartInterruptedCarriesOn(t *testing.T) {
	h := crashedTasks(t)
	tk := h.load(t, "A")
	r := tk.Repos[tk.RepoNames()[0]]
	r.Path = h.app.Store.Root() + "/no-such-repo"
	tk.SetRepo(tk.RepoNames()[0], r)
	if err := h.app.Store.Save(tk); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(r.Worktree); err != nil {
		t.Fatal(err)
	}
	h.out.Reset()
	err := h.exec(t, h.app.newStartCmd(), "--interrupted")
	if err == nil || !strings.Contains(err.Error(), "started 1 of 2") || !strings.Contains(err.Error(), "A did not start") {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(h.out.String(), "== B") || !strings.Contains(h.out.String(), "session tspace-B is up") {
		t.Fatalf("B not started:\n%s", h.out.String())
	}
}

func TestDashOffersInterrupted(t *testing.T) {
	h := crashedTasks(t)
	m := newDashModel(t.Context(), h.app, time.Second)
	m = drive(t, m, dashDataMsg(h.app.dashLoad(t.Context())))
	if m.confirm == nil || !strings.Contains(m.confirm.prompt, "2 tasks were interrupted") {
		t.Fatalf("confirm = %+v", m.confirm)
	}
	m = drive(t, m, key("n"))
	// Offered once: the next refresh does not ask again.
	m = drive(t, m, dashDataMsg(h.app.dashLoad(t.Context())))
	if m.confirm != nil {
		t.Fatal("asked twice")
	}
	m = drive(t, m, key("I"))
	m = drive(t, m, key("y"))
	if m.noteErr || !strings.Contains(m.note, "started A, B") {
		t.Fatalf("note %q", m.note)
	}
	m = drive(t, m, dashDataMsg(h.app.dashLoad(t.Context())))
	m = drive(t, m, key("I"))
	if m.confirm != nil || m.note != "no task was interrupted" {
		t.Fatalf("confirm=%+v note=%q", m.confirm, m.note)
	}
}
