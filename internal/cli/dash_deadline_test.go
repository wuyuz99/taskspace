package cli

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/wuyuz99/taskspace/internal/runtime"
	"github.com/wuyuz99/taskspace/internal/runtime/runtimetest"
	"github.com/wuyuz99/taskspace/internal/status"
)

// hungRuntime answers no query until it is given up on.
type hungRuntime struct{ *runtimetest.Fake }

func (hungRuntime) States(context.Context) (map[string]runtime.SessionRead, error) {
	return nil, errors.New("no batched answer")
}

func (hungRuntime) State(ctx context.Context, session string) (runtime.SessionState, error) {
	<-ctx.Done()
	return runtime.SessionState{Name: session, Panes: map[string]runtime.PaneState{}}, ctx.Err()
}

// A multiplexer that hangs costs a refresh, not every refresh after it.
func TestDashRefreshGivesUpOnAHungMultiplexer(t *testing.T) {
	h := newHarness(t)
	h.startedTask(t, "A-1")
	h.startedTask(t, "B-2")
	h.app.Runtime = hungRuntime{runtimetest.New()}
	defer func(d time.Duration) { dashRefreshLimit = d }(dashRefreshLimit)
	dashRefreshLimit = 50 * time.Millisecond

	done := make(chan dashData, 1)
	go func() { done <- h.app.dashLoad(context.Background()) }()
	var d dashData
	select {
	case d = <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("refresh did not give up")
	}
	if len(d.Warn) != 1 || !strings.Contains(d.Warn[0], "out of date") {
		t.Fatalf("warnings: %q", d.Warn)
	}
	if len(d.Rows) == 0 || d.Rows[0].Runtime != status.RuntimeUnknown {
		t.Fatalf("rows: %+v", d.Rows)
	}
}
