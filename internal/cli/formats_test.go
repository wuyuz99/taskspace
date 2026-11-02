package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	tsgit "github.com/wuyuz99/taskspace/internal/git"
	"github.com/wuyuz99/taskspace/internal/runtime"
	"github.com/wuyuz99/taskspace/internal/status"
	"github.com/wuyuz99/taskspace/internal/task"
)

// checkGolden compares got with a fixture. A fixture is a promise to whatever
// parses this output; see docs/FORMATS.md. Only adding to it is safe. Write a
// new one with TSPACE_UPDATE_GOLDEN=1.
func checkGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if os.Getenv("TSPACE_UPDATE_GOLDEN") != "" {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")), got) {
		t.Fatalf("%s changed; see docs/FORMATS.md\n--- got\n%s\n--- want\n%s", name, got, want)
	}
}

func TestStatusJSONFormat(t *testing.T) {
	at := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	signal := &task.Signal{State: task.SignalBlocked, Message: "needs a decision", At: at, Owner: "main"}
	tasks := []*task.Task{{Name: "PAY-142", Repos: map[string]task.Repo{"api": {Worktree: "/w/PAY-142/api"}}}}
	results := []status.TaskStatus{{
		Task: "PAY-142", Session: "tspace@PAY-142", Running: true,
		Root: &status.RootStatus{Agent: "claude", Runtime: status.RuntimeRunning,
			Signal: signal,
			Pane:   runtime.PaneState{Bell: true, Output: at},
			Report: &status.Report{State: status.ReportState(task.SignalBlocked), Source: status.SourceSignal, At: at, Message: "needs a decision"}},
		Repos: []status.RepoStatus{{
			Task: "PAY-142", Repo: "api", Branch: "agent/PAY-142", Agent: "shell",
			Runtime: status.RuntimeDead, Git: status.GitDirty,
			Detail:   tsgit.Status{Upstream: "origin/x", Staged: 1, Modified: 2, Untracked: 3, Conflicted: 4, Stashes: 5, Ahead: 6, Behind: 7},
			Unpushed: 8,
			Err:      errors.New("a problem"),
		}},
		Extra: []status.ExtraStatus{{Name: "_mon", Saved: true, Repo: "api", Command: []string{"top"}, Runtime: status.RuntimeInterrupted}},
	}, {
		Task: "OTHER", Session: "tspace@OTHER", Interrupted: true, RuntimeErr: errors.New("no multiplexer"),
	}}
	var out bytes.Buffer
	if err := (&App{Out: &out}).printStatusJSON(tasks, results); err != nil {
		t.Fatal(err)
	}
	checkGolden(t, "status.json", out.Bytes())
}
