package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aw-tool/aw/internal/runtime"
)

type openRuntime struct {
	stubRuntime
	calls               []string
	windows             []runtime.Window
	startErr, attachErr error
}

func (r *openRuntime) Start(_ context.Context, _ string, windows []runtime.Window) error {
	r.calls = append(r.calls, "start")
	r.windows = windows
	return r.startErr
}
func (r *openRuntime) State(context.Context, string) (runtime.SessionState, error) {
	r.calls = append(r.calls, "state")
	panes := map[string]runtime.PaneState{}
	for _, w := range r.windows {
		panes[w.Name] = runtime.PaneState{Dead: true}
	}
	return runtime.SessionState{Exists: true, Panes: panes}, nil
}
func (r *openRuntime) Attach(context.Context, string) error {
	r.calls = append(r.calls, "attach")
	return r.attachErr
}
func (r *openRuntime) Restart(context.Context, string, runtime.Window) error {
	panic("open must not restart existing agents")
}

func TestOpenPreparesAndResumesWorkspace(t *testing.T) {
	h := newHarness(t)
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-142", "--title", "Expired cards"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newRepoCmd(), "add", "PAY-142", h.repo, "--name", "backend"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newTaskCmd(), "handoff", "PAY-142", "Frontend next"); err != nil {
		t.Fatal(err)
	}
	rt := &openRuntime{}
	h.app.Runtime = rt
	if err := h.exec(t, h.app.newOpenCmd(), "PAY-142"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(rt.calls, ",") != "start,state,attach" {
		t.Fatalf("calls: %v", rt.calls)
	}
	if !strings.Contains(h.out.String(), "Expired cards") || !strings.Contains(h.out.String(), "Frontend next") {
		t.Fatalf("context missing: %s", h.out.String())
	}
	if !strings.Contains(h.errb.String(), "aw restart PAY-142 backend") {
		t.Fatalf("restart hint missing: %s", h.errb.String())
	}
	wt := h.app.Store.WorktreePath("PAY-142", "backend")
	if _, err := os.Stat(filepath.Join(wt, "README.md")); err != nil {
		t.Fatal(err)
	}
	wip := filepath.Join(wt, "wip.txt")
	if err := os.WriteFile(wip, []byte("keep this"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newOpenCmd(), "PAY-142"); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(wip); err != nil || string(b) != "keep this" {
		t.Fatal("reopening lost work")
	}
}

func TestOpenFailureDoesNotAttach(t *testing.T) {
	h := newHarness(t)
	h.startedTask(t, "t")
	rt := &openRuntime{startErr: errors.New("runtime failed")}
	h.app.Runtime = rt
	if err := h.exec(t, h.app.newOpenCmd(), "t"); !errors.Is(err, rt.startErr) {
		t.Fatalf("error: %v", err)
	}
	if strings.Join(rt.calls, ",") != "start" {
		t.Fatalf("attached after failure: %v", rt.calls)
	}
	rt.startErr = nil
	rt.attachErr = errors.New("attach failed")
	if err := h.exec(t, h.app.newOpenCmd(), "t"); !errors.Is(err, rt.attachErr) {
		t.Fatalf("attach error hidden: %v", err)
	}
}

func TestOpenEmptyTaskExplainsNextStep(t *testing.T) {
	h := newHarness(t)
	if _, err := h.app.Store.Create("empty"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newOpenCmd(), "empty"); err == nil || !strings.Contains(err.Error(), "aw repo add empty REPO") {
		t.Fatalf("error: %v", err)
	}
}
