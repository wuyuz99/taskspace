package status

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	tsexec "github.com/wuyuz99/taskspace/internal/exec"
	tsgit "github.com/wuyuz99/taskspace/internal/git"
	"github.com/wuyuz99/taskspace/internal/runtime"
	"github.com/wuyuz99/taskspace/internal/task"
)

// The sources are tried most trusted first: the agent's signal, then the
// bell, then a long silence, which is only ever a hint.
func TestReportTrustOrder(t *testing.T) {
	now := time.Unix(1700003600, 0)
	recent, long := now.Add(-time.Minute), now.Add(-QuietAfter-time.Minute)
	sig := func(s task.SignalState) *task.Signal {
		return &task.Signal{State: s, At: now.Add(-time.Hour), Message: "m"}
	}
	cases := []struct {
		name   string
		sig    *task.Signal
		state  RuntimeState
		pane   runtime.PaneState
		agent  bool
		want   ReportState
		source Source
	}{
		{"nothing", nil, RuntimeRunning, runtime.PaneState{Output: recent}, true, "", ""},
		{"signal", sig(task.SignalBlocked), RuntimeRunning, runtime.PaneState{Output: long}, true, "blocked", SourceSignal},
		{"bell", nil, RuntimeRunning, runtime.PaneState{Bell: true, Output: long}, true, ReportAttention, SourceBell},
		{"bell over working", sig(task.SignalWorking), RuntimeRunning, runtime.PaneState{Bell: true}, true, ReportAttention, SourceBell},
		{"done over bell", sig(task.SignalDone), RuntimeRunning, runtime.PaneState{Bell: true}, true, "done", SourceSignal},
		{"quiet", nil, RuntimeRunning, runtime.PaneState{Output: long}, true, ReportQuiet, SourceSilence},
		{"not quiet yet", nil, RuntimeRunning, runtime.PaneState{Output: recent}, true, "", ""},
		{"quiet shell", nil, RuntimeRunning, runtime.PaneState{Output: long}, false, "", ""},
		{"output unknown", nil, RuntimeRunning, runtime.PaneState{}, true, "", ""},
		{"dead keeps its signal", sig(task.SignalWorking), RuntimeDead, runtime.PaneState{Bell: true, Output: long}, true, "working", SourceSignal},
		{"dead rings nothing", nil, RuntimeDead, runtime.PaneState{Bell: true, Output: long}, true, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := report(c.sig, c.state, c.pane, nil, c.agent, now)
			if c.want == "" {
				if got != nil {
					t.Fatalf("report = %+v, want none", got)
				}
				return
			}
			if got == nil || got.State != c.want || got.Source != c.source {
				t.Fatalf("report = %+v, want %s from %s", got, c.want, c.source)
			}
		})
	}
}

// The aggregator reads the bell and output only from live windows, and
// measures silence by its own clock.
func TestTaskReportsFromThePane(t *testing.T) {
	now := time.Unix(1700003600, 0)
	tk := task.New("t")
	tk.Agent = []string{"codex"}
	tk.SetRepo("backend", task.Repo{Worktree: filepath.Join(t.TempDir(), "missing"), Agent: []string{"codex"}})
	tk.SetRepo("frontend", task.Repo{Worktree: filepath.Join(t.TempDir(), "missing")})
	rt := session(map[string]runtime.PaneState{
		task.RootWindowName: {Bell: true, Output: now.Add(-time.Minute)},
		"backend":           {Output: now.Add(-time.Hour)},
		"frontend":          {Output: now.Add(-time.Hour)},
	})
	a := New(tsgit.New(tsexec.Real()), rt)
	a.Now = func() time.Time { return now }
	got := a.Task(context.Background(), tk)

	if r := got.Root.Report; r == nil || r.Source != SourceBell || !got.Root.Pane.Bell {
		t.Errorf("root report = %+v, pane %+v", r, got.Root.Pane)
	}
	if r := got.Repos[0].Report; r == nil || r.State != ReportQuiet || !r.At.Equal(now.Add(-time.Hour)) {
		t.Errorf("backend report = %+v", r)
	}
	// frontend runs a shell, which is quiet by nature.
	if r := got.Repos[1].Report; r != nil {
		t.Errorf("frontend report = %+v, want none", r)
	}
}

// An agent's published status is its own account of the moment: it comes
// after its signal, but outranks a "working" signal it contradicts, and the
// bell, which may have rung long before.
func TestReportPublished(t *testing.T) {
	now := time.Unix(1700003600, 0)
	sig := func(s task.SignalState) *task.Signal { return &task.Signal{State: s, At: now.Add(-time.Hour)} }
	pub := func(s ReportState) *Published { return &Published{State: s, Source: "claude", Message: "m"} }
	working, blocked := ReportState(task.SignalWorking), ReportState(task.SignalBlocked)
	bell := runtime.PaneState{Bell: true, Output: now.Add(-QuietAfter * 2)}
	cases := []struct {
		name   string
		sig    *task.Signal
		state  RuntimeState
		pane   runtime.PaneState
		pub    *Published
		want   ReportState
		source Source
	}{
		{"alone", nil, RuntimeRunning, runtime.PaneState{}, pub(blocked), blocked, "claude"},
		{"over silence", nil, RuntimeRunning, runtime.PaneState{Output: now.Add(-time.Hour)}, pub(working), working, "claude"},
		{"over the bell", nil, RuntimeRunning, bell, pub(working), working, "claude"},
		{"under a blocked signal", sig(task.SignalBlocked), RuntimeRunning, bell, pub(working), blocked, SourceSignal},
		{"waiting over working", sig(task.SignalWorking), RuntimeRunning, runtime.PaneState{}, pub(blocked), blocked, "claude"},
		{"idle over working", sig(task.SignalWorking), RuntimeRunning, runtime.PaneState{}, pub(ReportIdle), ReportIdle, "claude"},
		{"agreeing with working", sig(task.SignalWorking), RuntimeRunning, bell, pub(working), working, SourceSignal},
		{"not from a dead window", nil, RuntimeDead, runtime.PaneState{}, pub(blocked), "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := report(c.sig, c.state, c.pane, c.pub, true, now)
			if c.want == "" {
				if got != nil {
					t.Fatalf("report = %+v, want none", got)
				}
				return
			}
			if got == nil || got.State != c.want || got.Source != c.source {
				t.Fatalf("report = %+v, want %s from %s", got, c.want, c.source)
			}
		})
	}
}

type fakePublished map[string]Published

func (f fakePublished) Published(context.Context) (map[string]Published, error) { return f, nil }

// Published status is matched to windows by the directory they run in: a
// repository's worktree, and the task directory for the root window.
func TestTaskMatchesPublishedByDirectory(t *testing.T) {
	root, wt := t.TempDir(), t.TempDir()
	tk := task.New("t")
	tk.Agent = []string{"claude"}
	tk.SetRepo("backend", task.Repo{Worktree: wt, Agent: []string{"claude"}})
	tk.SetRepo("frontend", task.Repo{Worktree: filepath.Join(t.TempDir(), "missing")})
	a := New(tsgit.New(tsexec.Real()), session(map[string]runtime.PaneState{
		task.RootWindowName: {}, "backend": {}, "frontend": {},
	}))
	a.TaskDir = func(string) string { return root }
	a.Published = []PublishedReader{fakePublished{
		root:                            {State: ReportIdle, Source: "claude"},
		wt + string(filepath.Separator): {State: ReportState(task.SignalBlocked), Source: "claude", Message: "Bash"},
	}}
	got := a.All(context.Background(), []*task.Task{tk})[0]
	if r := got.Root.Report; r == nil || r.State != ReportIdle {
		t.Errorf("root report = %+v", r)
	}
	if r := got.Repos[0].Report; r == nil || r.State != ReportState(task.SignalBlocked) || r.Message != "Bash" {
		t.Errorf("backend report = %+v", r)
	}
	if r := got.Repos[1].Report; r != nil {
		t.Errorf("frontend report = %+v, want none", r)
	}
}
