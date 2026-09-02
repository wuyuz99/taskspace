package exec

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// Call is one recorded invocation.
type Call struct {
	Dir  string
	Argv []string
}

func (c Call) String() string { return strings.Join(c.Argv, " ") }

// Fake is a Runner that records calls and replays canned responses. It exists
// so tests can assert on argument construction, which is where this tool's
// real bugs live.
//
// Responses are matched by substring against the joined argv, in insertion
// order; the first match wins. Unmatched calls succeed with empty output,
// so tests only need to script the commands they care about.
type Fake struct {
	mu       sync.Mutex
	calls    []Call
	handlers []handler
}

type handler struct {
	match string
	res   Result
	err   error
}

// NewFake returns an empty Fake.
func NewFake() *Fake { return &Fake{} }

// respond makes any command whose argv contains match return res.
func (f *Fake) respond(match string, res Result) *Fake {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handlers = append(f.handlers, handler{match: match, res: res})
	return f
}

// RespondOut scripts a command to succeed with the given stdout.
func (f *Fake) RespondOut(match, stdout string) *Fake {
	return f.respond(match, Result{Stdout: stdout})
}

// Fail makes any command whose argv contains match exit non-zero with stderr.
func (f *Fake) Fail(match, stderr string, code int) *Fake {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handlers = append(f.handlers, handler{
		match: match,
		res:   Result{Stderr: stderr, ExitCode: code},
		err:   errSentinel,
	})
	return f
}

var errSentinel = fmt.Errorf("fake: scripted failure")

// Run implements Runner.
func (f *Fake) Run(_ context.Context, dir string, argv ...string) (Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, Call{Dir: dir, Argv: argv})
	joined := strings.Join(argv, " ")

	for _, h := range f.handlers {
		if !strings.Contains(joined, h.match) {
			continue
		}
		if h.err != nil {
			return h.res, &ExitError{Argv: argv, Dir: dir, Result: h.res, ExitCode: h.res.ExitCode}
		}
		return h.res, nil
	}
	return Result{}, nil
}

// Calls returns every recorded invocation.
func (f *Fake) Calls() []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Call, len(f.calls))
	copy(out, f.calls)
	return out
}

// Commands returns each recorded invocation as a single space-joined string,
// which is the convenient form for test assertions.
func (f *Fake) Commands() []string {
	var out []string
	for _, c := range f.Calls() {
		out = append(out, c.String())
	}
	return out
}

// Ran reports whether any recorded command contains the given substring.
func (f *Fake) Ran(match string) bool {
	for _, c := range f.Commands() {
		if strings.Contains(c, match) {
			return true
		}
	}
	return false
}
