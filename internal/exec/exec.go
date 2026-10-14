// Package exec wraps subprocess execution behind a small interface so that
// argument construction can be tested without spawning processes.
//
// This is not dependency injection for its own sake: nearly all of tspace's real
// behaviour is "build the right argv for git or tmux", and that is exactly the
// thing that fails silently. Callers take a Runner and default to Real().
package exec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Result is the outcome of a finished command.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Out returns stdout with surrounding whitespace removed, which is what nearly
// every git and tmux call actually wants.
func (r Result) Out() string { return strings.TrimSpace(r.Stdout) }

// Runner executes a command in a working directory.
//
// A non-zero exit status is returned as an *ExitError, with Result still
// populated so callers can inspect stderr. Failure to start the process is
// returned as an ordinary error.
type Runner interface {
	Run(ctx context.Context, dir string, argv ...string) (Result, error)
}

// ExitError reports a command that ran and failed.
type ExitError struct {
	Argv     []string
	Dir      string
	Result   Result
	ExitCode int
}

func (e *ExitError) Error() string {
	msg := strings.TrimSpace(e.Result.Stderr)
	if msg == "" {
		msg = strings.TrimSpace(e.Result.Stdout)
	}
	if msg == "" {
		msg = fmt.Sprintf("exit status %d", e.ExitCode)
	}
	// Include the argv: we never paraphrase git, and the user should be able to
	// re-run exactly what we ran.
	return fmt.Sprintf("%s: %s", strings.Join(e.Argv, " "), msg)
}

// IsExit reports whether err is a command that ran and exited non-zero.
func IsExit(err error) bool {
	var ee *ExitError
	return errors.As(err, &ee)
}

type real struct{}

// Real returns a Runner that actually executes commands.
func Real() Runner { return real{} }

func (real) Run(ctx context.Context, dir string, argv ...string) (Result, error) {
	if len(argv) == 0 {
		return Result{}, errors.New("exec: empty argv")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	res := Result{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: cmd.ProcessState.ExitCode(),
	}

	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return res, &ExitError{Argv: argv, Dir: dir, Result: res, ExitCode: res.ExitCode}
	}
	if err != nil {
		return res, fmt.Errorf("exec %s: %w", strings.Join(argv, " "), err)
	}
	return res, nil
}
