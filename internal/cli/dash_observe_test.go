package cli

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A snapshot that fails during a refresh is drawn with the other warnings:
// the screen is Bubble Tea's, and anything on stderr would land on top of it.
func TestDashRefreshKeepsSnapshotWarningsOffStderr(t *testing.T) {
	h := newHarness(t)
	h.startedTask(t, "A-1")
	if err := h.exec(t, h.app.newStartCmd(), "A-1"); err != nil {
		t.Fatal(err)
	}
	// A non-empty directory in the snapshot's place, which nothing can replace.
	running := filepath.Join(h.app.Store.RunDir("A-1"), "running")
	if err := os.Remove(running); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(running, "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	h.errb.Reset()

	d := h.app.dashLoad(context.Background())

	if got := h.errb.String(); got != "" {
		t.Fatalf("written over the screen: %q", got)
	}
	if !slices.ContainsFunc(d.Warn, func(w string) bool { return strings.Contains(w, "record start") }) {
		t.Fatalf("warnings: %q", d.Warn)
	}
}
