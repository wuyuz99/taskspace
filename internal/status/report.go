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
	// ReportIdle means the agent says it is doing nothing: it finished a
	// turn, or has not been given one.
	ReportIdle ReportState = "idle"
)

// Source is where a report came from. The sources are tried in this order,
// most trusted first; a published status's source is the name of the
// integration that read it, such as "claude".
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
	State  ReportState `json:"state"`
	Source Source      `json:"source"`
	// At is when the agent signalled, or for the multiplexer's sources when
	// the window last printed. It is zero when that is not known.
	At      time.Time `json:"at,omitzero"`
	Message string    `json:"message,omitempty"`
}

// Published is what an agent says about itself through an interface of its
// own, read by an opt-in integration. It describes the agent as it is now.
type Published struct {
	// State is ReportState(task.SignalWorking), ReportState(task.SignalBlocked)
	// or ReportIdle.
	State   ReportState
	Message string
	Source  Source
}

// report picks a window's report from what is known of it, or nil when no
// source has anything to say.
//
// The sources are tried in order: the signal, the agent's published status,
// the bell, and silence. A signal saying "working" is the exception: the
// agent claimed to need nothing, so any later sign that it does, or that it
// stopped (a published status that is not working, or the bell), outranks
// it. Signals that already ask for a person (blocked, done, failed) say more
// than those do. Everything but the signal is read only from a window whose
// process is alive, and silence only where an agent runs: a shell is quiet
// by nature.
func report(sig *task.Signal, state RuntimeState, pane runtime.PaneState, pub *Published, agent bool, now time.Time) *Report {
	live := state == RuntimeRunning
	if !live {
		pub, pane = nil, runtime.PaneState{}
	}
	working := sig != nil && sig.State == task.SignalWorking
	pubWorking := pub != nil && pub.State == ReportState(task.SignalWorking)
	switch {
	case sig != nil && !working:
		return signalReport(sig)
	case pub != nil && (!working || !pubWorking):
		return &Report{State: pub.State, Source: pub.Source, Message: pub.Message}
	case pane.Bell && !pubWorking:
		return &Report{State: ReportAttention, Source: SourceBell, At: pane.Output}
	case sig != nil:
		return signalReport(sig)
	case agent && !pane.Output.IsZero() && now.Sub(pane.Output) >= QuietAfter:
		return &Report{State: ReportQuiet, Source: SourceSilence, At: pane.Output}
	}
	return nil
}

// Published says the report is an agent's published status, whose source
// is the integration that read it.
func (r *Report) Published() bool {
	switch r.Source {
	case SourceSignal, SourceBell, SourceSilence:
		return false
	}
	return true
}

func signalReport(sig *task.Signal) *Report {
	return &Report{State: ReportState(sig.State), Source: SourceSignal, At: sig.At, Message: sig.Message}
}
