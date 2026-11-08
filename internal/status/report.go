package status

import (
	"time"

	"github.com/wuyuz99/taskspace/internal/runtime"
	"github.com/wuyuz99/taskspace/internal/task"
)

// QuietAfter is how long a window must go without printing before its
// silence is offered as a hint that the agent may be waiting.
const QuietAfter = 10 * time.Minute

// ReportState is what a window's report says. The signal states mean what
// the agent said; the others are what tspace saw.
type ReportState string

const (
	// ReportAttention means the window rang the bell: something in it wants a
	// person, though not what.
	ReportAttention ReportState = "bell"
	// ReportQuiet means the window has printed nothing for QuietAfter. It is
	// a guess that the agent is waiting, never a claim that it is blocked.
	ReportQuiet ReportState = "quiet"
)

// Source is where a report came from. The sources are tried in this order,
// most trusted first.
type Source string

const (
	// SourceSignal is the agent's own word, through `tspace signal`.
	SourceSignal Source = "signal"
	// SourceBell is the multiplexer's bell flag on the window.
	SourceBell Source = "bell"
	// SourceSilence is how long the window has gone without printing.
	SourceSilence Source = "silence"
)

// Report is the one answer status gives to "what is this window's agent
// doing", from the most trusted source that has something to say, and says
// which source that was.
type Report struct {
	State  ReportState
	Source Source
	// At is when the agent signalled, or for the multiplexer's sources when
	// the window last printed. It is zero when that is not known.
	At      time.Time
	Message string
}

// report picks a window's report from what is known of it, or nil when no
// source has anything to say.
//
// The sources are tried in order: the signal, the bell, and silence. A
// signal saying "working" is the exception: the agent claimed to need
// nothing, so the bell, a later sign that it does, outranks it. Signals that
// already ask for a person (blocked, done, failed) say more than the bell
// does. Everything but the signal is read only from a window whose process
// is alive, and silence only where an agent runs: a shell is quiet by nature.
func report(sig *task.Signal, state RuntimeState, pane runtime.PaneState, agent bool, now time.Time) *Report {
	if state != RuntimeRunning {
		pane = runtime.PaneState{}
	}
	working := sig != nil && sig.State == task.SignalWorking
	switch {
	case sig != nil && !working:
		return signalReport(sig)
	case pane.Bell:
		return &Report{State: ReportAttention, Source: SourceBell, At: pane.Output}
	case sig != nil:
		return signalReport(sig)
	case agent && !pane.Output.IsZero() && now.Sub(pane.Output) >= QuietAfter:
		return &Report{State: ReportQuiet, Source: SourceSilence, At: pane.Output}
	}
	return nil
}

func signalReport(sig *task.Signal) *Report {
	return &Report{State: ReportState(sig.State), Source: SourceSignal, At: sig.At, Message: sig.Message}
}
