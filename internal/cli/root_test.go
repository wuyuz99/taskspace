package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/wuyuz99/taskspace/internal/task"
)

func TestWarnsAboutANearerRoot(t *testing.T) {
	h := newHarness(t)
	if err := h.app.Store.Init(); err != nil {
		t.Fatal(err)
	}
	inUse := filepath.Join(h.app.Store.WorktreesDir(), "PAY-1", "backend")
	other := task.NewStore(t.TempDir())
	if err := other.Init(); err != nil {
		t.Fatal(err)
	}
	elsewhere := filepath.Join(other.WorktreesDir(), "PAY-1", "backend")
	for _, d := range []string{inUse, elsewhere} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	h.app.warnNearerRoot(inUse)
	h.app.warnNearerRoot(t.TempDir())
	if h.errb.Len() != 0 {
		t.Fatalf("warned inside the root in use, or outside any: %s", h.errb.String())
	}
	// Something root-shaped inside the root in use, such as a repository with
	// tasks and worktrees folders, is still that root's.
	for _, d := range []string{"tasks", "worktrees"} {
		if err := os.MkdirAll(filepath.Join(inUse, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	h.app.warnNearerRoot(inUse)
	if h.errb.Len() != 0 {
		t.Fatalf("warned about a root nested in the one in use: %s", h.errb.String())
	}

	h.app.warnNearerRoot(elsewhere)
	if got := h.errb.String(); !strings.Contains(got, "TSPACE_ROOT="+other.Root()) {
		t.Fatalf("stderr: %q", got)
	}
}

func TestCompletionAndQuietSignalDoNotCheckTheRoot(t *testing.T) {
	a := &App{}
	root := &cobra.Command{Use: "tspace"}
	sig := a.newSignalCmd()
	completion := &cobra.Command{Use: "completion"}
	bash := &cobra.Command{Use: "bash"}
	completion.AddCommand(bash)
	complete := &cobra.Command{Use: "__complete"}
	status := &cobra.Command{Use: "status"}
	root.AddCommand(sig, completion, complete, status)

	if !checksRoot(status) || !checksRoot(sig) {
		t.Fatal("ordinary commands must check the root")
	}
	if checksRoot(bash) || checksRoot(complete) {
		t.Fatal("completion checked the root")
	}
	if err := sig.Flags().Set("quiet", "true"); err != nil {
		t.Fatal(err)
	}
	if checksRoot(sig) {
		t.Fatal("signal --quiet checked the root")
	}
}
