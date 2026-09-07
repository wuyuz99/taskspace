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
	Name      string          `json:"name"`
	Repos     map[string]Repo `json:"repos,omitempty"`
	Title     string          `json:"title,omitempty"`
	Ticket    string          `json:"ticket,omitempty"`
	TicketURL string          `json:"ticket_url,omitempty"`

	// Legacy fields, read once and cleared by Store.MigrateNotes. Records
	// written before notes became files carried the Markdown inline. Nothing
	// should read these directly.
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
}

// AgentLabel renders the agent for display.
func (r Repo) AgentLabel() string {
	if len(r.Agent) == 0 {
		return "shell"
	}
	return filepath.Base(r.Agent[0])
}

// New returns an empty task with the given name.
func New(name string) *Task {
	return &Task{Name: name, Repos: map[string]Repo{}}
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

// DefaultBranch is the branch convention for a task.
func DefaultBranch(taskName string) string { return "agent/" + taskName }

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
