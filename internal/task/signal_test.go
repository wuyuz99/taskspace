package task

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseSignalState(t *testing.T) {
	for _, st := range SignalStates {
		got, err := ParseSignalState(string(st))
		if err != nil || got != st {
			t.Errorf("ParseSignalState(%q) = %q, %v", st, got, err)
		}
	}
	_, err := ParseSignalState("sleeping")
	if err == nil {
		t.Fatal("unknown state accepted")
	}
	// The error lists the choices, so a typo can be fixed without the docs.
	if !strings.Contains(err.Error(), "working, blocked, done, failed") {
		t.Errorf("error = %v, want the states listed", err)
	}
}

func TestSignalRoundTripAndClear(t *testing.T) {
	s := newTestStore(t)

	none, err := s.ReadSignals("t")
	if err != nil || none != nil {
		t.Fatalf("signals of a task that has none = %v, %v", none, err)
	}

	before := time.Now().Add(-time.Second)
	if err := s.WriteSignal("t", "backend", SignalBlocked, "need a token"); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteSignal("t", RootWindowName, SignalWorking, ""); err != nil {
		t.Fatal(err)
	}
	// A rewrite replaces, it does not append.
	if err := s.WriteSignal("t", "backend", SignalDone, "merged"); err != nil {
		t.Fatal(err)
	}

	got, err := s.ReadSignals("t")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("signals = %+v, want two", got)
	}
	be := got["backend"]
	if be.State != SignalDone || be.Message != "merged" {
		t.Errorf("backend = %+v", be)
	}
	if be.At.Before(before) {
		t.Errorf("At = %v, want no earlier than %v", be.At, before)
	}
	if got[RootWindowName].State != SignalWorking {
		t.Errorf("root = %+v", got[RootWindowName])
	}

	entries, err := os.ReadDir(s.SignalsDir("t"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Errorf("signals dir holds %d entries, want 2 (no temp files)", len(entries))
	}

	if err := s.ClearSignal("t", "backend"); err != nil {
		t.Fatal(err)
	}
	// Clearing what is already gone is not an error.
	if err := s.ClearSignal("t", "backend"); err != nil {
		t.Fatalf("second clear: %v", err)
	}
	got, _ = s.ReadSignals("t")
	if _, ok := got["backend"]; ok || len(got) != 1 {
		t.Fatalf("after clear = %+v", got)
	}
}

// Signals are written by other programs; one bad file must not hide the rest.
func TestReadSignalsSkipsWhatItCannotUnderstand(t *testing.T) {
	s := newTestStore(t)
	if err := s.WriteSignal("t", "good", SignalFailed, "gave up"); err != nil {
		t.Fatal(err)
	}
	dir := s.SignalsDir("t")
	mustWrite(t, filepath.Join(dir, "garbled.json"), "{")
	mustWrite(t, filepath.Join(dir, "unknown.json"), `{"state":"sleeping"}`)
	mustWrite(t, filepath.Join(dir, ".partial.json"), `{"state":"done"}`)
	mustWrite(t, filepath.Join(dir, "notes.txt"), `{"state":"done"}`)
	if err := os.Mkdir(filepath.Join(dir, "sub.json"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := s.ReadSignals("t")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got["good"].State != SignalFailed {
		t.Fatalf("signals = %+v, want only the good one", got)
	}
}

func TestWriteSignalFailsUnderAFile(t *testing.T) {
	s := newTestStore(t)
	// A file where the signals directory should be.
	if err := os.MkdirAll(s.ContextDir("t"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, s.SignalsDir("t"), "")
	if err := s.WriteSignal("t", "w", SignalDone, ""); err == nil {
		t.Fatal("WriteSignal succeeded with a file in place of its directory")
	}
}

func TestWriteSignalRejectsAnUnsafeTaskName(t *testing.T) {
	s := newTestStore(t)
	if err := s.WriteSignal("../escape", "w", SignalDone, ""); err == nil {
		t.Fatal("unsafe task name accepted")
	}
}

// A signal describes a running agent, so stopping the session clears them all,
// but notes beside them in .context must survive.
func TestClearSignalsKeepsNotes(t *testing.T) {
	s := newTestStore(t)
	if err := s.WriteSignal("t", "w", SignalWorking, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteNote("t", BriefFile, "keep me\n"); err != nil {
		t.Fatal(err)
	}
	if err := s.ClearSignals("t"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.SignalsDir("t")); !os.IsNotExist(err) {
		t.Fatalf("signals dir survived: %v", err)
	}
	if n, _ := s.ReadNote("t", BriefFile); n.Text != "keep me\n" {
		t.Fatalf("note lost: %q", n.Text)
	}

	// With no note left, the context directory goes too.
	if err := s.WriteNote("t", BriefFile, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteSignal("t", "w", SignalWorking, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.ClearSignals("t"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.ContextDir("t")); !os.IsNotExist(err) {
		t.Fatalf("empty context dir survived: %v", err)
	}
}
