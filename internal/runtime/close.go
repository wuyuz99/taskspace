package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// WindowCloser is an optional capability, keeping protocol-1 adapters and
// runtimes usable for whole-session operations without per-window support.
type WindowCloser interface {
	CloseWindow(context.Context, string, string) error
}

// CloseWindow interrupts window's live panes, gives them StopGrace to exit,
// then kills the window. A window that is not there is already closed; tmux
// ends the session with its last window.
func (t *Tmux) CloseWindow(ctx context.Context, session, window string) error {
	exists, err := t.hasWindow(ctx, session, window)
	if err != nil || !exists {
		return err
	}
	target := windowTarget(session, WindowName(window))
	res, err := t.tmux(ctx, "list-panes", "-t", target, "-F", livePaneFormat)
	if err != nil {
		return err
	}
	interrupted := false
	for _, line := range strings.Split(res.Stdout, "\n") {
		id, dead, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if ok && dead != "1" {
			if _, err := t.tmux(ctx, "send-keys", "-t", id, "C-c"); err == nil {
				interrupted = true
			}
		}
	}
	if interrupted {
		if err := waitForExit(ctx, t.StopGrace); err != nil {
			return err
		}
	}
	if _, err := t.tmux(ctx, "kill-window", "-t", target); err != nil {
		if exists, check := t.hasWindow(ctx, session, window); check == nil && !exists {
			return nil
		}
		return err
	}
	return nil
}

// waitForExit waits out grace, or until ctx is done.
func waitForExit(ctx context.Context, grace time.Duration) error {
	if grace <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// CloseWindow closes window's tab as Tmux.CloseWindow closes its window.
func (z *Zellij) CloseWindow(ctx context.Context, session, window string) error {
	panes, _, err := z.panes(ctx, session)
	if err != nil {
		return err
	}
	tab, interrupted := -1, false
	for _, p := range panes {
		if p.TabName != WindowName(window) {
			continue
		}
		tab = p.TabID
		if !p.Exited {
			if _, err := z.action(ctx, session, "send-keys", "--pane-id", p.target(), "Ctrl c"); err == nil {
				interrupted = true
			}
		}
	}
	if tab < 0 {
		return nil
	}
	if interrupted {
		if err := waitForExit(ctx, z.StopGrace); err != nil {
			return err
		}
	}
	if _, err := z.action(ctx, session, "close-tab-by-id", fmt.Sprint(tab)); err != nil {
		if st, check := z.State(ctx, session); check == nil {
			if _, found := st.Panes[WindowName(window)]; !found {
				return nil
			}
		}
		return err
	}
	return nil
}

// CloseWindow runs the adapter's optional close operation. An adapter
// without it fails here, and never falls back to stopping the session.
func (a *Adapter) CloseWindow(ctx context.Context, session, window string) error {
	if _, err := a.call(ctx, "close", session, WindowName(window)); err != nil {
		return fmt.Errorf("close window %s (adapter may not support optional close operation): %w", window, err)
	}
	return nil
}
