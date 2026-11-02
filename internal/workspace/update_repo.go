package workspace

import (
	"context"
	"fmt"
	"slices"

	"github.com/wuyuz99/taskspace/internal/task"
)

// UpdateRepoOptions say what UpdateRepo changes; what is left unset stays.
type UpdateRepoOptions struct {
	// SetAgent replaces the agent with Agent; Shell clears it.
	SetAgent bool
	Agent    []string
	Shell    bool
	// SetResume replaces the resume command with Resume; NoResume forgets it.
	SetResume bool
	Resume    []string
	NoResume  bool
	// SetHook replaces the post_create hook with PostCreate.
	SetHook    bool
	PostCreate string
	// Branch renames the task's branch, and Base changes its base, moving
	// its commits there with Rebase.
	Branch, Base string
	Rebase       bool
}

// UpdateRepoResult is what UpdateRepo did.
type UpdateRepoResult struct {
	Task *task.Task
	// Notes say what changed in git.
	Notes []string
	// TaskAgent says the repository's window runs the task's agent, so that
	// is what changed. ResumeCleared says a resume command went with an agent
	// it no longer fits.
	TaskAgent, ResumeCleared bool
}

// UpdateRepo changes a repository's settings under the task's lock. Git goes
// last, once nothing else can refuse.
func (s *Service) UpdateRepo(ctx context.Context, name, repo string, opts UpdateRepoOptions) (*UpdateRepoResult, error) {
	// Renaming or rebasing the branch changes the worktree, so it is a
	// lifecycle change; the rest only edits the record.
	t, ctx, unlock, err := s.Lock(ctx, name, s.command("tspace repo update"), opts.Branch != "" || opts.Rebase)
	if err != nil {
		return nil, err
	}
	defer unlock()
	r, ok := t.Repo(repo)
	if !ok {
		return nil, t.NoRepo(repo)
	}
	// The agent repository's window runs the task's agent, unless it has one
	// of its own, so that is what its agent flags change.
	taskAgent := repo == t.AgentRepo && len(r.Agent) == 0
	// A shell there would leave the task with no agent anywhere, and take its
	// resume command along.
	if taskAgent && opts.Shell && len(t.Agent) > 0 {
		return nil, fmt.Errorf("%s's window runs the task's agent, so --shell would drop it; to run a shell there, drop the task's agent with tspace task update %s --shell", repo, t.Name)
	}
	before := t.RepoAgent(repo)
	switch {
	case taskAgent && opts.SetAgent:
		t.Agent = opts.Agent
	case opts.SetAgent:
		r.Agent = opts.Agent
	case opts.Shell:
		r.Agent = nil
	}
	after := r.Agent
	if taskAgent {
		after = t.Agent
	}
	agentChanged := (opts.SetAgent || opts.Shell) && !slices.Equal(before, after)
	var cleared bool
	if taskAgent {
		t.Resume, cleared = nextResume(opts.SetResume, opts.Resume, opts.NoResume, t.Resume, agentChanged)
	} else {
		r.Resume, cleared = nextResume(opts.SetResume, opts.Resume, opts.NoResume, r.Resume, agentChanged)
	}
	if opts.SetHook {
		r.PostCreate = opts.PostCreate
	}
	t.SetRepo(repo, r)
	if len(t.ResumeFor(repo)) > 0 && len(t.RepoAgent(repo)) == 0 {
		return nil, fmt.Errorf("--resume resumes the window's agent, and %s runs a shell; give one with --agent", repo)
	}
	var notes []string
	if opts.Branch != "" || opts.Base != "" {
		record := func(r task.Repo) error {
			t.SetRepo(repo, r)
			return s.Store.Save(t)
		}
		if r, notes, err = s.MoveRepoBranch(ctx, t, repo, r, opts.Branch, opts.Base, opts.Rebase, record); err != nil {
			return nil, err
		}
		t.SetRepo(repo, r)
	}
	if err := s.Store.Save(t); err != nil {
		return nil, err
	}
	return &UpdateRepoResult{Task: t, Notes: notes, TaskAgent: taskAgent, ResumeCleared: cleared}, nil
}
