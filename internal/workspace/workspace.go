// Package workspace carries out a task's lifecycle: preparing its worktrees
// and starting its session, stopping and restarting it, and cleaning it up.
//
// The CLI and the dashboard both drive it, so the workflow and its safety
// decisions live in one place. It parses no arguments and lays nothing out:
// what an operation does on the way is reported as Events, in order, for the
// caller to show as it likes, and what it ends with as a result or an error.
// Errors and Warning texts are sentences, as Go errors are, and name the
// tspace command that fixes the problem where there is one. What hooks print
// goes straight to Stdout and Stderr, as they print it.
package workspace

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	tsgit "github.com/wuyuz99/taskspace/internal/git"
	"github.com/wuyuz99/taskspace/internal/runtime"
	"github.com/wuyuz99/taskspace/internal/task"
)

// Service runs lifecycle operations against a store, git and a runtime.
type Service struct {
	Store   *task.Store
	Git     *tsgit.Git
	Runtime runtime.Runtime

	// Settle is how long Start waits before checking that what it launched is
	// still running.
	Settle time.Duration
	// Stdout and Stderr receive what post_create hooks print.
	Stdout, Stderr io.Writer
	// Progress is told what an operation does as it does it; nil discards.
	Progress func(Event)

	// NoWait makes an operation on a task another command is changing fail at
	// once, naming that command, instead of waiting for it. Waiting, if set,
	// is told when an operation has to wait, and who for.
	NoWait  bool
	Waiting func(name string, holder task.LockHolder)

	// Command is the tspace command running the operation, such as
	// "tspace open", named to whoever has to wait for it. Empty, each
	// operation names its own.
	Command string

	// Carriers are the conversation carriers the caller knows, by agent
	// name. Only those the config's carry_conversations names run.
	Carriers map[string]ConversationCarrier
}

// command is the name an operation runs under: Command, else its own.
func (s *Service) command(own string) string {
	if s.Command != "" {
		return s.Command
	}
	return own
}

// Event is something an operation did or found, reported as it happened.
type Event interface{ event() }

// Warning is a problem that did not stop the operation, ready to show.
type Warning struct{ Text string }

// WorktreeReady reports a repository's worktree is there for Start.
type WorktreeReady struct {
	Repo   string
	Branch string
	Result tsgit.EnsureResult
}

// HookStarted and HookFailed bracket a post_create hook; its output goes to
// Stdout and Stderr in between.
type HookStarted struct{ Repo, Command string }
type HookFailed struct {
	Repo string
	Err  error
}

// Launching reports Start is about to bring up the session: with its root
// window when Root, and with Repos' windows.
type Launching struct {
	Task  *task.Task
	Root  bool
	Repos []string
}

// WindowExited reports a window that Start found dead right after launch;
// Window is task.RootWindowName for the root window.
type WindowExited struct{ Task, Window string }

// Resumed reports a window launched with its resume command, Command, to
// bring its agent back with its conversation.
type Resumed struct {
	Task, Window string
	Command      []string
}

// StartingTask, TaskStarted and TaskNotStarted bracket each task
// StartInterrupted starts.
type StartingTask struct{ Task string }
type TaskStarted struct{ Result *StartResult }
type TaskNotStarted struct {
	Task string
	Err  error
}

// ResumeFailed reports a resumed window whose command exited at once, which
// was relaunched with its own command, fresh.
type ResumeFailed struct{ Task, Window string }

// NoRepositories reports Clean found nothing to remove.
type NoRepositories struct{ Task string }

// SessionStopped reports Clean ended the task's session.
type SessionStopped struct{ Session string }

// WorktreeRemoved reports Clean removed a repository's worktree; NoWorktree
// that it found none to remove.
type WorktreeRemoved struct{ Repo string }
type NoWorktree struct{ Repo string }

// BranchKept reports Clean left a branch it was asked to delete, because
// its work is not safe elsewhere. Orphaned says no task will refer to it.
type BranchKept struct {
	Repo, Branch, Reason, Path string
	Orphaned                   bool
}

// BranchDeleted and BranchNotDeleted report Clean's branch deletions.
type BranchDeleted struct{ Repo, Branch, Reason string }
type BranchNotDeleted struct {
	Repo, Branch string
	Err          error
}

// Cleaned reports Clean removed the worktrees; Removing says the record and
// notes go next.
type Cleaned struct {
	Task     string
	Repos    []string // set when only these repositories were cleaned
	Removing bool
}

// ConversationCarried reports the carrier for Agent moved the conversation
// history kept for From to To, which From moved to. Window is
// task.RootWindowName for the task root.
type ConversationCarried struct{ Task, Window, Agent, From, To string }

// AgentPlaced reports SettleLayout gave a task's first repository, Repo, its
// agent, Command. RootWindowPending reports it gave the task a root window
// for the next start to open, and RootWindowOpened that OpenRootWindow opened
// one at Dir; either way the task's agent stays in AgentRepo.
type AgentPlaced struct {
	Task, Repo string
	Command    []string
}
type RootWindowPending struct{ Task, AgentRepo string }
type RootWindowOpened struct{ Task, Dir, AgentRepo string }

// RecordRemoved and NotesRemoved report Delete.
type RecordRemoved struct{ Task string }
type NotesRemoved struct {
	Task  string
	Files []string
}

func (Warning) event()             {}
func (WorktreeReady) event()       {}
func (HookStarted) event()         {}
func (HookFailed) event()          {}
func (Launching) event()           {}
func (WindowExited) event()        {}
func (Resumed) event()             {}
func (ResumeFailed) event()        {}
func (StartingTask) event()        {}
func (TaskStarted) event()         {}
func (TaskNotStarted) event()      {}
func (NoRepositories) event()      {}
func (SessionStopped) event()      {}
func (WorktreeRemoved) event()     {}
func (NoWorktree) event()          {}
func (BranchKept) event()          {}
func (BranchDeleted) event()       {}
func (BranchNotDeleted) event()    {}
func (Cleaned) event()             {}
func (RecordRemoved) event()       {}
func (NotesRemoved) event()        {}
func (ConversationCarried) event() {}
func (AgentPlaced) event()         {}
func (RootWindowPending) event()   {}
func (RootWindowOpened) event()    {}

func (s *Service) report(e Event) {
	if s.Progress != nil {
		s.Progress(e)
	}
}

func (s *Service) warnf(format string, args ...any) {
	s.report(Warning{Text: fmt.Sprintf(format, args...)})
}

// Lock takes the task's lock for command, then loads the task, so what the
// caller saves was read under the lock; task.Lock has the rules. The returned
// context carries the lock, so operations given it take it again for free.
//
// lifecycle is for operations that change worktrees or the session: they
// refuse a lock inherited from the tspace that runs them, such as a
// post_create hook running clean on the task its start is creating.
func (s *Service) Lock(ctx context.Context, name, command string, lifecycle bool) (*task.Task, context.Context, func(), error) {
	if err := task.ValidateName("task", name); err != nil {
		return nil, ctx, nil, err
	}
	// No lock file for a name that is not a task.
	if !s.Store.Exists(name) {
		return nil, ctx, nil, fmt.Errorf("%q: %w", name, task.ErrNotFound)
	}
	ctx, l, err := s.Acquire(ctx, name, command)
	if err != nil {
		return nil, ctx, nil, err
	}
	if lifecycle && l.Inherited() {
		l.Unlock()
		return nil, ctx, nil, fmt.Errorf("%s cannot run from a hook of the tspace that is already changing %s; it may only update the task's record", command, name)
	}
	// Not upgraded here: the operation's own save does that, and one that
	// saves nothing, like stop, must work whatever a migration would hit.
	t, err := s.Store.Load(name)
	if err != nil {
		l.Unlock()
		return nil, ctx, nil, err
	}
	return t, ctx, l.Unlock, nil
}

// Acquire takes name's lock for command, which need not be a task yet, and
// returns ctx carrying it.
func (s *Service) Acquire(ctx context.Context, name, command string) (context.Context, *task.Lock, error) {
	try := ctx
	var busy *task.LockHolder
	if s.NoWait {
		var cancel context.CancelFunc
		try, cancel = context.WithCancel(ctx)
		cancel() // one attempt
	}
	l, err := s.Store.Lock(try, name, command+" "+name, func(h task.LockHolder) {
		if s.NoWait {
			busy = &h
			return
		}
		if s.Waiting != nil {
			s.Waiting(name, h)
		}
	})
	if err != nil {
		if busy != nil {
			return ctx, nil, fmt.Errorf("%s is busy: %s is changing it; try again when it is done", name, *busy)
		}
		return ctx, nil, err
	}
	// A lock re-entered is already in the context, which keeps the one that
	// holds the file.
	if !l.Reentered() {
		ctx = task.WithLock(ctx, l)
	}
	return ctx, l, nil
}

// Every window tspace launches is told which task and window it is, so that
// signal reports for the window the agent runs in wherever it has cd'd to.
const (
	EnvTask   = "TSPACE_TASK"
	EnvWindow = "TSPACE_WINDOW"
)

// WindowEnv is the environment a task's window is launched with. An empty
// window names none, which a window started from inside another one needs, so
// as not to inherit that one's.
//
// TSPACE_ROOT is set too, so that tspace run in the window uses the state that
// launched it whatever environment the multiplexer's server was started with.
func WindowEnv(st *task.Store, taskName, window string) []string {
	return []string{EnvTask + "=" + taskName, EnvWindow + "=" + window, task.EnvRoot + "=" + st.Root()}
}

// RootWindow is the task's window at its workspace root.
func RootWindow(st *task.Store, t *task.Task) runtime.Window {
	return runtime.Window{Name: task.RootWindowName, Dir: st.TaskDir(t.Name), Command: ExpandArgv(st, t, t.RootAgent()), Env: WindowEnv(st, t.Name, task.RootWindowName)}
}

// RepoWindow is a repository's window, in its worktree.
//
// An agent running there cannot see the task's notes, which sit beside the
// worktree in .context: clean refuses untracked files, so they cannot be
// copied in. It finds them at ../.context or through TSPACE_CONTEXT_DIR, or
// its command names them with {context_dir}; see ExpandArgv.
func RepoWindow(st *task.Store, t *task.Task, name string) runtime.Window {
	env := append(WindowEnv(st, t.Name, name), "TSPACE_CONTEXT_DIR="+st.ContextDir(t.Name))
	return runtime.Window{Name: name, Dir: t.Repos[name].Worktree, Command: ExpandArgv(st, t, t.RepoAgent(name)), Env: env}
}

// ExtraWindow is a window saved with t as its session runs it: in its
// repository's worktree or at the task root, running its command or, closing
// when it exits, the user's shell. An extra window has no signal of its own,
// so an agent in it falls back to the directory.
func ExtraWindow(st *task.Store, t *task.Task, w task.Window) runtime.Window {
	dir := st.TaskDir(t.Name)
	if w.Repo != "" {
		dir = t.Repos[w.Repo].Worktree
	}
	argv := ExpandArgv(st, t, w.Command)
	return runtime.Window{Name: w.SessionName(), Dir: dir, Command: argv, Transient: len(argv) == 0, Env: WindowEnv(st, t.Name, "")}
}

// KnownWindow is the extra window named name as tspace knows it: saved with
// t, or remembered from when it was opened. A window tspace did not open is
// not known, nor is one whose repository has since left the task.
func KnownWindow(st *task.Store, t *task.Task, name string) (task.Window, bool) {
	if i, ok := t.SavedWindow(name); ok {
		return t.Windows[i], true
	}
	w, ok := st.OpenedWindow(t.Name, name)
	if !ok {
		return task.Window{}, false
	}
	if _, ok := t.Repo(w.Repo); w.Repo != "" && !ok {
		return task.Window{}, false
	}
	return w, true
}

// ExpandArgv fills in the task's directories where an agent command names
// them, so any agent can be pointed at them with its own flag, such as
// `claude --add-dir {context_dir}`, without tspace knowing about that agent:
//
//	{context_dir}  the task's .context directory, holding TASK.md and HANDOFF.md
//	{task_dir}     the task's directory, holding every worktree
func ExpandArgv(st *task.Store, t *task.Task, argv []string) []string {
	r := strings.NewReplacer("{context_dir}", st.ContextDir(t.Name), "{task_dir}", st.TaskDir(t.Name))
	out := make([]string, len(argv))
	for i, a := range argv {
		out[i] = r.Replace(a)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// WorktreeDir resolves a task's repository, or its only one when repoArg is
// empty, to a worktree that exists.
func WorktreeDir(t *task.Task, repoArg string) (task.Repo, string, error) {
	r, name, err := ResolveRepo(t, repoArg)
	if err != nil {
		return r, name, err
	}
	if info, err := os.Stat(r.Worktree); err != nil || !info.IsDir() {
		return task.Repo{}, "", fmt.Errorf("worktree for %s is missing (%s); run: tspace start %s",
			name, r.Worktree, t.Name)
	}
	return r, name, nil
}

// ResolveRepo resolves a task's repository, or its only one when repoArg is
// empty, whether or not its worktree exists yet.
func ResolveRepo(t *task.Task, repoArg string) (task.Repo, string, error) {
	names := t.RepoNames()
	if len(names) == 0 {
		return task.Repo{}, "", fmt.Errorf("task %q has no repositories", t.Name)
	}

	name := repoArg
	if name == "" {
		if len(names) > 1 {
			return task.Repo{}, "", fmt.Errorf("task %q has several repos (%s); name one",
				t.Name, strings.Join(names, ", "))
		}
		name = names[0]
	}
	r, ok := t.Repo(name)
	if !ok {
		return task.Repo{}, "", fmt.Errorf("task %q has no repo named %q (has: %s)",
			t.Name, name, strings.Join(names, ", "))
	}
	return r, name, nil
}

// ensureContextDir creates the notes directory before any agent starts: a
// command may name it with {context_dir}, and an agent given a directory
// that does not exist may refuse to start.
func (s *Service) ensureContextDir(t *task.Task) error {
	return os.MkdirAll(s.Store.ContextDir(t.Name), 0o755)
}
