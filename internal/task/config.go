package task

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
)

// Config holds user-wide defaults.
//
// Defaults are applied when a task is created and then recorded on the task,
// the same way a project recipe is snapshotted: changing the config later never
// changes what an existing task runs.
type Config struct {
	// Agent is the task-root agent for new tasks that name none themselves.
	Agent []string `json:"agent,omitempty"`
	// Resume is the default agent's resume command, recorded on a new task
	// together with Agent, and only when Agent is what the task got.
	Resume []string `json:"resume,omitempty"`
	// Repos maps a short name to a source repository's canonical path, so a
	// task can be given "backend" instead of a path every time.
	Repos map[string]string `json:"repos,omitempty"`
	// BranchPrefix replaces "agent/" in new tasks' default branch names. A
	// pointer because an empty prefix — branches named after the task alone —
	// is a real choice, distinct from never having set one.
	BranchPrefix *string `json:"branch_prefix,omitempty"`
	// Multiplexer names the terminal multiplexer sessions run in: a built-in
	// ("tmux", the default, or "zellij") or any name with an adapter. Unlike
	// the defaults above it is not recorded on tasks: it is where tspace looks
	// for every task's session.
	Multiplexer string `json:"multiplexer,omitempty"`
	// Adapters maps a multiplexer name to the command of its adapter, for
	// adapters not on PATH as tspace-mux-NAME.
	Adapters map[string][]string `json:"adapters,omitempty"`
	// TicketPattern is a regular expression that finds the ticket key in a new
	// task's name, for tasks given no --ticket; see TicketFrom. Off when empty:
	// tspace knows no ticket system's keys.
	TicketPattern string `json:"ticket_pattern,omitempty"`
	// Terminal is the command that opens a new terminal tab or window running
	// a command, for dash; see `tspace config terminal`. tspace knows no
	// terminal's own way of doing that.
	Terminal []string `json:"terminal,omitempty"`
}

// TicketGroup names the part of a ticket pattern that is the ticket, when the
// whole match is not. Only a named group: plain parentheses are as often there
// for alternation, as in (ENG|OPS)-[0-9]+.
const TicketGroup = "ticket"

// CompileTicketPattern checks a ticket pattern: it must compile and must not
// match an empty name, or the leftmost match could be empty in a name that
// holds a ticket.
func CompileTicketPattern(pattern string) (*regexp.Regexp, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("ticket pattern: %w", err)
	}
	if re.MatchString("") {
		return nil, fmt.Errorf("ticket pattern %q matches an empty name; it must match at least one character", pattern)
	}
	return re, nil
}

// TicketFrom is the ticket the pattern finds in a task name: the group named
// TicketGroup if the pattern has one and it matched, else the whole match, else
// "". An invalid pattern, as from an edited config file, is an error.
func (c *Config) TicketFrom(name string) (string, error) {
	if c.TicketPattern == "" {
		return "", nil
	}
	re, err := CompileTicketPattern(c.TicketPattern)
	if err != nil {
		return "", err
	}
	m := re.FindStringSubmatch(name)
	if m == nil {
		return "", nil
	}
	if i := re.SubexpIndex(TicketGroup); i > 0 && m[i] != "" {
		return m[i], nil
	}
	return m[0], nil
}

// DefaultBranchPrefix prefixes a task's branch when the config names none.
const DefaultBranchPrefix = "agent/"

// Branch is the default branch for a new repository entry in task taskName.
func (c *Config) Branch(taskName string) string {
	if c.BranchPrefix != nil {
		return *c.BranchPrefix + taskName
	}
	return DefaultBranch(taskName)
}

// RepoNames returns the registered repository names, sorted.
func (c *Config) RepoNames() []string {
	names := make([]string, 0, len(c.Repos))
	for n := range c.Repos {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// ConfigPath is the user's config file.
func (s *Store) ConfigPath() string { return filepath.Join(s.root, "config.json") }

// LoadConfig reads the config. A missing file is an empty config, not an error.
func (s *Store) LoadConfig() (*Config, error) {
	b, err := os.ReadFile(s.ConfigPath())
	if errors.Is(err, fs.ErrNotExist) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", s.ConfigPath(), err)
	}
	return &c, nil
}

// SaveConfig replaces the config file atomically.
func (s *Store) SaveConfig(c *Config) error {
	return publishJSON(s.ConfigPath(), c, true)
}
