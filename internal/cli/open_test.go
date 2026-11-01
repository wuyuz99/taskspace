package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wuyuz99/taskspace/internal/runtime/runtimetest"
)

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
	rt := h.fake()
	rt.ExitAtStart("backend")
	if err := h.exec(t, h.app.newOpenCmd(), "PAY-142"); err != nil {
		t.Fatal(err)
	}
	if len(started(rt)) == 0 || len(rt.Calls(runtimetest.OpAttach)) != 1 {
		t.Fatalf("calls: %v", rt.Calls())
	}
	if !strings.Contains(h.out.String(), "Expired cards") || !strings.Contains(h.out.String(), "Frontend next") {
		t.Fatalf("context missing: %s", h.out.String())
	}
	if !strings.Contains(h.errb.String(), "tspace restart PAY-142 backend") {
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
	if got := restarted(rt); len(got) != 0 {
		t.Fatalf("open restarted existing agents: %+v", got)
	}
}

func TestOpenFailureDoesNotAttach(t *testing.T) {
	h := newHarness(t)
	h.startedTask(t, "t")
	rt := h.fake()
	startErr, attachErr := errors.New("runtime failed"), errors.New("attach failed")
	rt.Fail(runtimetest.OpStart, startErr)
	if err := h.exec(t, h.app.newOpenCmd(), "t"); !errors.Is(err, startErr) {
		t.Fatalf("error: %v", err)
	}
	if got := rt.Calls(runtimetest.OpAttach); len(got) != 0 {
		t.Fatalf("attached after failure: %v", got)
	}
	rt.Fail(runtimetest.OpStart, nil)
	rt.Fail(runtimetest.OpAttach, attachErr)
	if err := h.exec(t, h.app.newOpenCmd(), "t"); !errors.Is(err, attachErr) {
		t.Fatalf("attach error hidden: %v", err)
	}
}

func TestOpenEmptyTaskExplainsNextStep(t *testing.T) {
	h := newHarness(t)
	if _, err := h.app.Store.Create("empty"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newOpenCmd(), "empty"); err == nil || !strings.Contains(err.Error(), "tspace repo add empty REPO") {
		t.Fatalf("error: %v", err)
	}
}
