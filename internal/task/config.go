package task

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Config holds user-wide defaults.
//
// Defaults are applied when a task is created and then recorded on the task,
// the same way a project recipe is snapshotted: changing the config later never
// changes what an existing task runs.
type Config struct {
	// Agent is the task-root agent for new tasks that name none themselves.
	Agent []string `json:"agent,omitempty"`
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
