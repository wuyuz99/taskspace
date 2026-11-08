package workspace

import (
	"context"
	"fmt"
	"strings"

	"github.com/wuyuz99/taskspace/internal/task"
)

// CreateTaskOptions describe a new task. What is left unset comes from the
// project, then the user's config.
type CreateTaskOptions struct {
	Name string
	// Repos are repositories to attach, each as ParseRepoSpec reads them,
	// and Refs those to attach as references.
	Repos, Refs []string
	// Project, with SetProject, snapshots that saved recipe into the task.
	Project    string
	SetProject bool

	Title, Kind, TicketURL string
	// Ticket, with SetTicket, replaces the ticket found in Name.
	Ticket    string
	SetTicket bool
	// Brief, with SetBrief, is stored as the task's brief.
	Brief    string
	SetBrief bool

	// SetAgent makes Agent the task's agent; Shell gives it none.
	SetAgent bool
	Agent    []string
	Shell    bool
	// SetResume sets its resume command to Resume; NoResume forgets it.
	SetResume bool
	Resume    []string
	NoResume  bool

	// WithRoot keeps the task-root window for a single-repository task.
	WithRoot bool
	// Start starts the task once it is created, under the same lock.
	Start bool
}

// CreateTaskResult is what CreateTask made.
type CreateTaskResult struct {
	Task *task.Task
	// Started is what Start brought up, if it was asked to and did.
	Started *StartResult
}

// TaskCreated reports a task's record made. AgentFrom names where its agent
// came from, unless the caller chose it, and Starting says a start follows.
type TaskCreated struct {
	Task      *task.Task
	AgentFrom string
	Starting  bool
}

func (TaskCreated) event() {}

// CreateTask records a new task and the repositories it spans, and starts it
// if asked. Nothing is published until every repository checks out, and what
// each reported is held until then, so a later one that fails does not follow
// "added backend" for a task that was never made.
func (s *Service) CreateTask(ctx context.Context, o CreateTaskOptions) (*CreateTaskResult, error) {
	name := o.Name
	if err := task.ValidateName("task", name); err != nil {
		return nil, err
	}
	if s.Store.Exists(name) {
		return nil, fmt.Errorf("%q: %w", name, task.ErrExists)
	}
	t := task.New(name)
	t.Title, t.Kind, t.TicketURL = o.Title, o.Kind, o.TicketURL
	if o.SetTicket {
		t.Ticket = o.Ticket
	}
	// Loaded only when something needs it, so a task that names its agent and
	// no repositories can be created with a broken config.
	var cfg *task.Config
	config := func() (*task.Config, error) {
		if cfg == nil {
			c, err := s.Store.LoadConfig()
			if err != nil {
				return nil, err
			}
			cfg = c
		}
		return cfg, nil
	}
	var held []Event
	adding := *s
	adding.Progress = func(e Event) {
		if _, warning := e.(Warning); warning {
			s.report(e)
			return
		}
		held = append(held, e)
	}
	// The root agent comes from the options, else the recipe, else the user's
	// default. Whichever wins is recorded on the task.
	agentFrom := ""
	if o.SetProject {
		p, err := s.Store.LoadProject(o.Project)
		if err != nil {
			return nil, err
		}
		t.Agent = append([]string(nil), p.Agent...)
		t.Resume = append([]string(nil), p.Resume...)
		t.Windows = task.CopyWindows(p.Windows)
		if len(p.Agent) > 0 {
			agentFrom = "project " + p.Name
		}
		for _, repoName := range p.RepoNames() {
			r := p.Repos[repoName]
			c, err := config()
			if err != nil {
				return nil, err
			}
			if err := adding.AddRepo(ctx, t, c, ExpandHome(r.Path), AddRepoOptions{Name: repoName, Base: r.Base, Agent: r.Agent, Resume: r.Resume, PostCreate: r.PostCreate, Reference: r.Reference}); err != nil {
				return nil, err
			}
		}
	}
	// Each repository is sugar for a following `tspace repo add`.
	for _, spec := range o.Repos {
		c, err := config()
		if err != nil {
			return nil, err
		}
		if err := adding.AddRepoSpec(ctx, t, c, spec, AddRepoOptions{}); err != nil {
			return nil, err
		}
	}
	for _, spec := range o.Refs {
		c, err := config()
		if err != nil {
			return nil, err
		}
		if err := adding.AddRepoSpec(ctx, t, c, spec, AddRepoOptions{Reference: true}); err != nil {
			return nil, err
		}
	}
	// Like the agent, a broken config does not stop a task that needs nothing
	// from it.
	if !o.SetTicket {
		c, err := config()
		if err == nil {
			t.Ticket, err = c.TicketFrom(name)
		}
		if err != nil {
			s.warnf("note: no ticket read from the name: %v", err)
		}
	}
	// A resume command goes with the agent it resumes: the recipe's or the
	// config's comes along only with that agent.
	switch {
	case o.SetAgent || o.Shell:
		t.Agent = nil
		if o.SetAgent {
			t.Agent = o.Agent
		}
		agentFrom = ""
		t.Resume = nil
	case len(t.Agent) == 0:
		c, err := config()
		if err != nil {
			return nil, err
		}
		if len(c.Agent) > 0 {
			t.Agent = append([]string(nil), c.Agent...)
			t.Resume = append([]string(nil), c.Resume...)
			agentFrom = "tspace config"
		}
	}
	switch {
	case o.SetResume:
		t.Resume = o.Resume
	case o.NoResume:
		t.Resume = nil
	}
	if len(t.Resume) > 0 && len(t.Agent) == 0 {
		return nil, fmt.Errorf("--resume resumes the task's agent, and it has none; give one with --agent")
	}
	if o.WithRoot {
		t.RootWindow = true
	} else if len(t.Repos) == 1 && t.CanRunAgentIn(t.RepoNames()[0]) {
		t.AgentRepo = t.RepoNames()[0]
	}
	if o.Start && len(t.Repos) == 0 {
		return nil, fmt.Errorf("--start needs a repository to start; name one, or add it later and run: tspace start %s", name)
	}
	// Held from here through Start. Publishing never replaces a record, so of
	// two creates at once only one gets this far.
	ctx, l, err := s.Acquire(ctx, name, s.command("tspace task create"))
	if err != nil {
		return nil, err
	}
	defer l.Unlock()
	if err := s.Store.CreatePrepared(t); err != nil {
		return nil, err
	}
	// The brief is written after the record exists, so a failure here leaves
	// a task without a brief rather than a note without a task.
	if o.SetBrief {
		if err := s.Store.WriteNote(t.Name, task.BriefFile, o.Brief); err != nil {
			return nil, err
		}
	}
	for _, e := range held {
		s.report(e)
	}
	s.report(TaskCreated{Task: t, AgentFrom: agentFrom, Starting: o.Start})
	result := &CreateTaskResult{Task: t}
	if o.Start {
		if result.Started, err = s.Start(ctx, t.Name, StartOptions{}); err != nil {
			return result, fmt.Errorf("task %s was created, but starting it failed: %w", t.Name, err)
		}
	}
	return result, nil
}

// RegisteredHint says what to do about a repository name that is not
// registered.
func RegisteredHint(c *task.Config) string {
	if len(c.Repos) == 0 {
		return "; register it with: tspace config repo NAME PATH"
	}
	return " (registered: " + strings.Join(c.RepoNames(), ", ") + ")"
}
