package workspace

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/wuyuz99/taskspace/internal/task"
)

// UpdateTaskOptions say what UpdateTask changes; what is left unset stays.
type UpdateTaskOptions struct {
	SetTitle, SetKind, SetTicket, SetTicketURL bool
	Title, Kind, Ticket, TicketURL             string
	// SetBrief stores Brief as the task's brief.
	SetBrief bool
	Brief    string
	// SetAgent replaces the task's agent with Agent; Shell clears it.
	SetAgent bool
	Agent    []string
	Shell    bool
	// SetResume replaces the resume command with Resume; NoResume forgets it.
	SetResume bool
	Resume    []string
	NoResume  bool
	// AgentAtRoot moves the agent to the root window, and SetAgentIn to
	// AgentIn's window.
	AgentAtRoot bool
	SetAgentIn  bool
	AgentIn     string
}

// AgentMove is what moving a task's agent did. From is the window it ran in
// and To the one it runs in now, the same if it stayed. HadRoot says the task
// had a root window before. Conversation says a conversation stays with From.
// Running says the session is up, so the agent is still in From.
type AgentMove struct {
	From, To              string
	HadRoot               bool
	Conversation, Running bool
}

// UpdateTaskResult is what UpdateTask did. ResumeCleared says a resume command
// went with an agent it no longer fits, and AgentSet that the agent was
// replaced or cleared. Move is set if asked to move the agent.
type UpdateTaskResult struct {
	Task          *task.Task
	AgentSet      bool
	ResumeCleared bool
	Move          *AgentMove
}

// UpdateTask changes a task's details and agent under its lock. Running
// windows are left alone: the agent keeps running where it is until they are
// created or restarted.
func (s *Service) UpdateTask(ctx context.Context, name string, o UpdateTaskOptions) (*UpdateTaskResult, error) {
	t, ctx, unlock, err := s.Lock(ctx, name, s.command("tspace task update"), false)
	if err != nil {
		return nil, err
	}
	defer unlock()
	from, hadRoot := t.AgentWindow(), t.HasRootWindow()
	if o.SetAgentIn {
		if o.AgentIn == task.RootWindowName {
			return nil, fmt.Errorf("%s is the root window; use --agent-at-root", o.AgentIn)
		}
		if _, ok := t.Repo(o.AgentIn); !ok {
			return nil, t.NoRepo(o.AgentIn)
		}
		if !t.CanRunAgentIn(o.AgentIn) {
			return nil, fmt.Errorf("%s has an agent of its own, which its window runs; clear it first with tspace repo update %s %s --shell", o.AgentIn, t.Name, o.AgentIn)
		}
		t.AgentRepo = o.AgentIn
		// A root window there was may be running; it stays, as a shell.
		t.RootWindow = hadRoot
	}
	if o.AgentAtRoot {
		t.AgentRepo = ""
		// Kept from now on, as RemoveRepo keeps it: the agent's history is in
		// the task directory.
		t.RootWindow = true
	}
	if o.SetTitle {
		t.Title = o.Title
	}
	if o.SetKind {
		t.Kind = o.Kind
	}
	if o.SetTicket {
		t.Ticket = o.Ticket
	}
	if o.SetTicketURL {
		t.TicketURL = o.TicketURL
	}
	res := &UpdateTaskResult{Task: t, AgentSet: o.SetAgent || o.Shell}
	before := t.Agent
	if res.AgentSet {
		t.Agent = nil
		if o.SetAgent {
			t.Agent = o.Agent
		}
	}
	t.Resume, res.ResumeCleared = nextResume(o.SetResume, o.Resume, o.NoResume, t.Resume, res.AgentSet && !slices.Equal(before, t.Agent))
	if len(t.Resume) > 0 && len(t.Agent) == 0 {
		return nil, fmt.Errorf("--resume resumes the task's agent, and it has none; give one with --agent")
	}
	if err := s.Store.Save(t); err != nil {
		return nil, err
	}
	// A running session shows the new title at once; one that is not running
	// gets it when it starts.
	if o.SetTitle && s.Runtime != nil {
		_ = s.Runtime.SetLabel(ctx, s.Runtime.SessionName(t.Name), t.Label())
	}
	if o.SetBrief {
		if err := s.Store.WriteNote(t.Name, task.BriefFile, o.Brief); err != nil {
			return nil, err
		}
	}
	if o.AgentAtRoot || o.SetAgentIn {
		res.Move = s.agentMoved(ctx, t, from, hadRoot)
	}
	return res, nil
}

// agentMoved brings run data up to date with the agent having moved from
// window from.
func (s *Service) agentMoved(ctx context.Context, t *task.Task, from string, hadRoot bool) *AgentMove {
	m := &AgentMove{From: from, To: t.AgentWindow(), HadRoot: hadRoot}
	if m.To == from {
		return m
	}
	// Whatever ran in the new window before, a shell or another agent, was not
	// this agent: it starts there fresh rather than resume a conversation that
	// is not its own. restart --resume still resumes on request.
	if err := s.Store.ForgetLaunched(t.Name, m.To); err != nil {
		s.warnf("tspace: %v", err)
	}
	if len(t.Agent) == 0 {
		return m
	}
	// An agent that resumes by directory finds its conversation where it ran.
	m.Conversation = len(t.Resume) > 0 && s.Store.Launched(t.Name, from)
	running, err := s.SessionUp(ctx, t.Name)
	if err != nil {
		s.warnf("tspace: %v; if it is running, the agent moves when its windows are restarted", err)
		return m
	}
	m.Running = running
	return m
}

// nextResume is the resume command to record. A changed agent drops the old
// one, which resumed a different program, unless a new one is given; cleared
// says so.
func nextResume(set bool, argv []string, none bool, current []string, agentChanged bool) (resume []string, cleared bool) {
	switch {
	case set:
		return argv, false
	case none:
		return nil, false
	case agentChanged:
		return nil, len(current) > 0
	}
	return current, false
}

// RemoveTask deletes a task's record and notes, refusing while its worktrees
// are on disk: tearing those down is Clean. force deletes the record anyway,
// and leaves the worktrees and the notes describing them where they are.
func (s *Service) RemoveTask(ctx context.Context, name string, force bool) error {
	t, _, unlock, err := s.Lock(ctx, name, s.command("tspace task rm"), true)
	if err != nil {
		return err
	}
	defer unlock()
	if force {
		if err := s.DeleteRecord(t); err != nil {
			return err
		}
		if note, err := s.Store.ReadNote(t.Name, task.BriefFile); err == nil && note.Text != "" {
			s.warnf("note: the task's notes were left at %s", s.Store.ContextDir(t.Name))
		}
		return nil
	}
	var present []string
	for _, repo := range t.RepoNames() {
		if info, err := os.Stat(t.Repos[repo].Worktree); err == nil && info.IsDir() {
			present = append(present, repo)
		}
	}
	if len(present) > 0 {
		return fmt.Errorf("task %q still has worktrees for %s\nrun `tspace clean %s` first, or `tspace task rm %s --force` to drop the record and leave them on disk",
			t.Name, strings.Join(present, ", "), t.Name, t.Name)
	}
	return s.Delete(t)
}

// WriteNote replaces one of the task's notes, or with empty text clears it.
func (s *Service) WriteNote(ctx context.Context, name, file, text string) (*task.Task, error) {
	t, _, unlock, err := s.Lock(ctx, name, s.command("tspace task note"), false)
	if err != nil {
		return nil, err
	}
	defer unlock()
	return t, s.Store.WriteNote(t.Name, file, text)
}
