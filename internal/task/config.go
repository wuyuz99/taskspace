package task

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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
	// Repos maps a short name to a source repository's canonical path, so a
	// task can be given "backend" instead of a path every time.
	Repos map[string]string `json:"repos,omitempty"`
	// BranchPrefix replaces "agent/" in new tasks' default branch names. A
	// pointer because an empty prefix — branches named after the task alone —
	// is a real choice, distinct from never having set one.
	BranchPrefix *string `json:"branch_prefix,omitempty"`
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
