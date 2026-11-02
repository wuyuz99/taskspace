package task

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SignalState is what an agent says it is doing.
//
// The runtime can only see whether a process is alive. Whether that process is
// busy, waiting on a person or finished is known to the agent alone, so the
// agent reports it. A signal is a claim, not an observation: it is shown beside
// the runtime state, never instead of it.
type SignalState string

const (
	// SignalWorking means the agent is busy and needs nothing.
	SignalWorking SignalState = "working"
	// SignalBlocked means the agent is waiting on a person.
	SignalBlocked SignalState = "blocked"
	// SignalDone means the agent has finished what it was asked.
	SignalDone SignalState = "done"
	// SignalFailed means the agent gave up.
	SignalFailed SignalState = "failed"
)

// SignalOwnerMain is the owner of a blocked signal a hook set for the agent's
// main thread, as opposed to one of its subagents.
const SignalOwnerMain = "main"

// SignalStates lists the states in the order help text shows them.
var SignalStates = []SignalState{SignalWorking, SignalBlocked, SignalDone, SignalFailed}

// ParseSignalState reads a state as typed on the command line.
func ParseSignalState(s string) (SignalState, error) {
	for _, st := range SignalStates {
		if s == string(st) {
			return st, nil
		}
	}
	names := make([]string, len(SignalStates))
	for i, st := range SignalStates {
		names[i] = string(st)
	}
	return "", fmt.Errorf("unknown state %q (one of: %s)", s, strings.Join(names, ", "))
}

// Signal is the latest thing a window's agent reported.
//
// At is the file's modification time, as for notes, so an agent that writes the
// file itself needs to get only the state right.
type Signal struct {
	State   SignalState
	Message string
	At      time.Time
	// Owner is set only on a blocked signal from a hook, and names the thread
	// of the agent that is waiting: SignalOwnerMain, or a subagent's id. Only
	// that thread's hook takes the signal back; see `tspace signal --help`.
	Owner string
}

// signalFile is a signal as stored: the time is the file's own.
type signalFile struct {
	State   SignalState `json:"state"`
	Message string      `json:"message,omitempty"`
	Owner   string      `json:"owner,omitempty"`
}

// SignalsDir holds one file per window. It sits in .context so that an agent
// in a worktree can write ../.context/signals/REPO.json without tspace on its PATH.
func (s *Store) SignalsDir(taskName string) string {
	return filepath.Join(s.ContextDir(taskName), "signals")
}

// SignalPath is where a window's signal lives. Window is a repository name, or
// RootWindowName for the task root.
func (s *Store) SignalPath(taskName, window string) string {
	return filepath.Join(s.SignalsDir(taskName), window+".json")
}

// WriteSignal replaces a window's signal.
func (s *Store) WriteSignal(taskName, window string, state SignalState, message string) error {
	return s.WriteOwnedSignal(taskName, window, state, message, "")
}

// WriteOwnedSignal replaces a window's signal with one that records its owner.
func (s *Store) WriteOwnedSignal(taskName, window string, state SignalState, message, owner string) error {
	if err := ValidateName("task", taskName); err != nil {
		return err
	}
	b, err := json.Marshal(signalFile{State: state, Message: message, Owner: owner})
	if err != nil {
		return err
	}
	// Through a temp file, so a reader never sees half a signal. Not synced: a
	// hook runs on every tool, and a signal lost to a crash described an agent
	// the crash ended.
	if err := writeFile(s.SignalPath(taskName, window), append(b, '\n'), 0o600, false); err != nil {
		return fmt.Errorf("write signal: %w", err)
	}
	return nil
}

// ReadSignals returns the task's signals by window.
//
// A file that cannot be understood is skipped: signals are written by other
// programs, and one of them getting it wrong must not hide the rest.
func (s *Store) ReadSignals(taskName string) (map[string]Signal, error) {
	dir := s.SignalsDir(taskName)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}
	out := map[string]Signal{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		var f signalFile
		if json.Unmarshal(b, &f) != nil {
			continue
		}
		if _, err := ParseSignalState(string(f.State)); err != nil {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out[strings.TrimSuffix(name, ".json")] = Signal{State: f.State, Message: f.Message, At: info.ModTime().UTC(), Owner: f.Owner}
	}
	return out, nil
}

// ClearSignal forgets one window's signal.
func (s *Store) ClearSignal(taskName, window string) error {
	if err := os.Remove(s.SignalPath(taskName, window)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("clear signal: %w", err)
	}
	return nil
}

// ClearSignals forgets every signal of a task. A signal describes a running
// agent, so it goes when the session does.
func (s *Store) ClearSignals(taskName string) error {
	if err := os.RemoveAll(s.SignalsDir(taskName)); err != nil {
		return fmt.Errorf("clear signals: %w", err)
	}
	// The context directory goes too, but only if no note is in it.
	_ = os.Remove(s.ContextDir(taskName))
	return nil
}
