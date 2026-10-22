package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"

	tsexec "github.com/wuyuz99/taskspace/internal/exec"
)

type paneReplies struct {
	outputs []string
	calls   int
}

func (r *paneReplies) Run(_ context.Context, _ string, _ ...string) (tsexec.Result, error) {
	i := min(r.calls, len(r.outputs)-1)
	r.calls++
	return tsexec.Result{Stdout: r.outputs[i]}, nil
}

func TestZellijEmptyReplyRetries(t *testing.T) {
	r := &paneReplies{outputs: []string{" \n\t", terminalPanes("backend")}}
	st, err := NewZellij(r).State(t.Context(), "session")
	if err != nil || !st.Exists || len(st.Panes) != 1 || r.calls != 2 {
		t.Fatalf("state=%+v calls=%d err=%v", st, r.calls, err)
	}
}

func TestZellijEmptyReplyBounded(t *testing.T) {
	r := &paneReplies{outputs: []string{""}}
	st, err := NewZellij(r).State(t.Context(), "session")
	if err == nil || !strings.Contains(err.Error(), "returned no output") || !st.Exists || r.calls != 4 {
		t.Fatalf("state=%+v calls=%d err=%v", st, r.calls, err)
	}
}

func TestZellijEmptyReplyCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	r := &paneReplies{outputs: []string{""}}
	_, err := NewZellij(r).State(ctx, "session")
	if !errors.Is(err, context.Canceled) || r.calls != 1 {
		t.Fatalf("calls=%d err=%v", r.calls, err)
	}
}

func TestZellijMalformedReplyNotRetried(t *testing.T) {
	r := &paneReplies{outputs: []string{"invalid"}}
	_, err := NewZellij(r).State(t.Context(), "session")
	if err == nil || r.calls != 1 {
		t.Fatalf("calls=%d err=%v", r.calls, err)
	}
}
