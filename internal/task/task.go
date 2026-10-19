// Package task holds the declarative model for a logical task and its
// persistence.
//
// Only declarative metadata lives here. Runtime state — whether a session is
// up, whether a worktree is dirty — is deliberately absent: it is queried live
// from tmux and git on every command, so there is nothing to go stale and
// nothing to reconcile.
package task

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Task is a logical unit of work spanning one or more repositories.
//
// The record holds identity and configuration. The task's prose — its brief and
// its handoff — lives in Markdown files under the task's .context directory,
// where a person and an agent in a worktree can both reach it. See notes.go.
type Task struct {
	// Version is the record's format; see CurrentVersion.
	Version   int             `json:"version,omitempty"`
	Name      string          `json:"name"`
	Repos     map[string]Repo `json:"repos,omitempty"`
	Title     string          `json:"title,omitempty"`
	Ticket    string          `json:"ticket,omitempty"`
	TicketURL string          `json:"ticket_url,omitempty"`
	// Agent is the command for the task's root window, as argv. That window
	// opens in the directory holding every worktree, where one agent can reach
	// every repository. Empty means the user's shell.
	Agent []string `json:"agent,omitempty"`
	// Resume is the command that brings Agent back with its conversation, for
	// restart --resume: `claude --continue`, say. Each agent has its own way,
	// so tspace knows none; empty means the agent cannot be resumed.
	Resume []string `json:"resume,omitempty"`
	// AgentRepo, when set, is the repository whose window runs Agent, in its
	// worktree, in place of a task-root window. task create sets it for a task
	// with one repository, so its agent works in the code rather than a
	// directory above it.
	AgentRepo string `json:"agent_repo,omitempty"`
	// RootWindow says the task has a task-root window although AgentRepo runs
	// its agent; that window runs a shell. It is set when a second repository
	// is added, or by task create --with-root, and never cleared on its own: a
	// running root window must not vanish because a repository went.
	RootWindow bool `json:"root_window,omitempty"`

	// Legacy fields of a version 0 record, which carried the Markdown inline
	// before notes became files; the upgrade to version 1 moves them out.
	// Only that migration, and a read of an outdated record, look at them.
	//
	// The timestamp is a pointer because `omitempty` does nothing for a struct:
	// as a plain time.Time it wrote "0001-01-01T00:00:00Z" into every record
	// that never had a handoff.
	LegacyBrief            string     `json:"brief,omitempty"`
	LegacyHandoff          string     `json:"handoff,omitempty"`
	LegacyHandoffUpdatedAt *time.Time `json:"handoff_updated_at,omitempty"`
}

// Repo is one repository enrolled in a task.
type Repo struct {
	// Path is the canonical absolute path of the source repository.
	Path string `json:"path"`
	// Branch is the branch checked out in the worktree, by default agent/<task>.
	Branch string `json:"branch"`
	// Base is the ref the branch was created from, recorded at creation time so
	// that later safety checks compare against the same ref.
	Base string `json:"base,omitempty"`
	// Worktree is the absolute path of this repo's worktree for the task.
	Worktree string `json:"worktree"`
	// PostCreate is an optional shell command run once in a freshly created
	// worktree, for the .env / node_modules / venv problem.
	PostCreate string `json:"post_create,omitempty"`
	// Agent is the command to launch, as argv. Empty means the user's shell.
	Agent []string `json:"agent,omitempty"`
	// Resume brings Agent back with its conversation; see Task.Resume.
	Resume []string `json:"resume,omitempty"`
}

// AgentLabel renders the agent for display.
func (r Repo) AgentLabel() string {
	if len(r.Agent) == 0 {
		return "shell"
	}
	return filepath.Base(r.Agent[0])
}

// RootWindowName names the task's root window. Repository names must start with
// a letter or digit, so no repository window can ever be called this.
const RootWindowName = "_root"

// AgentLabel renders the root window's agent for display.
func (t *Task) AgentLabel() string { return Repo{Agent: t.RootAgent()}.AgentLabel() }

// HasRootWindow reports whether the task's session has a task-root window.
func (t *Task) HasRootWindow() bool { return t.AgentRepo == "" || t.RootWindow }

// RootAgent is the command of the task-root window: the task's agent, unless
// that runs in AgentRepo's window, which leaves the root window a shell.
func (t *Task) RootAgent() []string {
	if t.AgentRepo != "" {
		return nil
	}
	return t.Agent
}

// RepoAgent is the command a repository's window runs: its own agent, or
// for AgentRepo without one, the task's.
func (t *Task) RepoAgent(name string) []string {
	r := t.Repos[name]
	if name == t.AgentRepo && len(r.Agent) == 0 {
		return t.Agent
	}
	return r.Agent
}

// ResumeFor is the command that resumes the agent in a window, or nil if
// there is none: the task's for the window running the task's agent, else the
// repository's own.
//
// It follows RepoAgent and RootAgent, so a resume command always belongs to
// the program the window runs.
func (t *Task) ResumeFor(window string) []string {
	if window == RootWindowName {
		if t.AgentRepo != "" {
			return nil // a shell, beside an agent that runs in AgentRepo
		}
		return t.Resume
	}
	r := t.Repos[window]
	if window == t.AgentRepo && len(r.Agent) == 0 {
		return t.Resume
	}
	return r.Resume
}

// WindowAgent is the command a window runs, root or repository.
func (t *Task) WindowAgent(window string) []string {
	if window == RootWindowName {
		return t.RootAgent()
	}
	return t.RepoAgent(window)
}

// AgentWindow names the window running the task's agent.
func (t *Task) AgentWindow() string {
	if t.AgentRepo != "" {
		return t.AgentRepo
	}
	return RootWindowName
}

// RemoveRepo forgets a repository. Removing AgentRepo moves the task's agent
// back to the task-root window. Either way the task keeps a root window from
// then on: one may have run an agent whose history is in the task directory,
// so a later first repository must not move the agent into a worktree.
func (t *Task) RemoveRepo(name string) {
	delete(t.Repos, name)
	if name == t.AgentRepo {
		t.AgentRepo = ""
	}
	if t.AgentRepo == "" {
		t.RootWindow = true
	}
}

// CanRunAgentIn reports whether repository name can be AgentRepo: one with
// an agent of its own would leave the task's agent nowhere to run.
func (t *Task) CanRunAgentIn(name string) bool {
	r, ok := t.Repos[name]
	return ok && len(r.Agent) == 0
}

// New returns an empty task with the given name.
func New(name string) *Task {
	return &Task{Version: CurrentVersion, Name: name, Repos: map[string]Repo{}}
}

// RepoNames returns the repo names in stable sorted order, so that output and
// tmux window ordering are deterministic.
func (t *Task) RepoNames() []string {
	names := make([]string, 0, len(t.Repos))
	for n := range t.Repos {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Repo looks up a repo by name.
func (t *Task) Repo(name string) (Repo, bool) {
	r, ok := t.Repos[name]
	return r, ok
}

// SetRepo adds or replaces a repo entry.
func (t *Task) SetRepo(name string, r Repo) {
	if t.Repos == nil {
		t.Repos = map[string]Repo{}
	}
	t.Repos[name] = r
}

// DefaultBranch is the branch convention for a task when the config sets no
// prefix of its own.
func DefaultBranch(taskName string) string { return DefaultBranchPrefix + taskName }

// nameRe restricts names to characters that are safe in a path segment and a
// git branch name.
//
// '.' is permitted because repositories legitimately have dots in their names.
// tmux reads '.' and ':' as target syntax (session:window.pane), so the runtime
// layer sanitizes names when building a session name; ':' is rejected outright
// here because it is also awkward in paths and offers nothing.
var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// ValidateName rejects names that would be unsafe as a filename, a branch
// component, or a tmux session name.
func ValidateName(kind, name string) error {
	switch {
	case name == "":
		return fmt.Errorf("%s name is empty", kind)
	case name == "." || name == "..":
		return fmt.Errorf("%s name %q is not allowed", kind, name)
	case strings.ContainsAny(name, `/\`):
		return fmt.Errorf("%s name %q must not contain a path separator", kind, name)
	case !nameRe.MatchString(name):
		return fmt.Errorf("%s name %q must start with a letter or digit and contain only letters, digits, '.', '_' or '-'", kind, name)
	}
	return nil
}
