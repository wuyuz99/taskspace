package task

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Project contains only reusable configuration, never task identity or progress.
type Project struct {
	Name  string                 `json:"name"`
	Repos map[string]ProjectRepo `json:"repos"`
	// Agent is the task-root agent argv. Empty leaves the root window a shell.
	Agent []string `json:"agent,omitempty"`
	// Resume is Agent's resume command; see Task.Resume.
	Resume []string `json:"resume,omitempty"`
	// Windows are the extra windows tasks made from it get; see Task.Windows.
	Windows []Window `json:"windows,omitempty"`
}

type ProjectRepo struct {
	Path       string   `json:"path"`
	Base       string   `json:"base,omitempty"`
	Agent      []string `json:"agent,omitempty"`
	Resume     []string `json:"resume,omitempty"`
	PostCreate string   `json:"post_create,omitempty"`
}

func ProjectFromTask(name string, t *Task) *Project {
	p := &Project{Name: name, Repos: map[string]ProjectRepo{}, Agent: append([]string(nil), t.Agent...), Resume: append([]string(nil), t.Resume...)}
	for repoName, r := range t.Repos {
		p.Repos[repoName] = ProjectRepo{Path: r.Path, Base: r.Base, Agent: append([]string(nil), r.Agent...), Resume: append([]string(nil), r.Resume...), PostCreate: r.PostCreate}
	}
	p.Windows = CopyWindows(t.Windows)
	return p
}

// CopyWindows copies saved windows, commands and all.
func CopyWindows(ws []Window) []Window {
	if len(ws) == 0 {
		return nil
	}
	out := make([]Window, len(ws))
	for i, w := range ws {
		out[i] = Window{Name: w.Name, Repo: w.Repo, Command: append([]string(nil), w.Command...)}
	}
	return out
}

func (p *Project) RepoNames() []string {
	names := make([]string, 0, len(p.Repos))
	for name := range p.Repos {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (p *Project) validate() error {
	if err := ValidateName("project", p.Name); err != nil {
		return err
	}
	if len(p.Repos) == 0 {
		return fmt.Errorf("project %q has no repositories", p.Name)
	}
	for name, r := range p.Repos {
		if err := ValidateName("repo", name); err != nil {
			return err
		}
		if strings.TrimSpace(r.Path) == "" {
			return fmt.Errorf("project repository %s has no path", name)
		}
	}
	return nil
}

func (s *Store) ProjectsDir() string { return filepath.Join(s.root, "projects") }

func (s *Store) LoadProject(name string) (*Project, error) {
	if err := ValidateName("project", name); err != nil {
		return nil, err
	}
	b, err := os.ReadFile(filepath.Join(s.ProjectsDir(), name+".json"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("no project saved as %q (saved: %s)", name, s.projectNames())
	}
	if err != nil {
		return nil, fmt.Errorf("read project %q: %w", name, err)
	}
	var p Project
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, fmt.Errorf("parse project %q: %w", name, err)
	}
	p.Name = name
	if err := p.validate(); err != nil {
		return nil, err
	}
	return &p, nil
}

// SaveProject publishes a complete file. Without replace, a concurrent save
// cannot overwrite another recipe: linking the temporary file is exclusive.
func (s *Store) SaveProject(p *Project, replace bool) error {
	if err := p.validate(); err != nil {
		return err
	}
	return publishJSON(filepath.Join(s.ProjectsDir(), p.Name+".json"), p, replace)
}

// projectNames lists the saved recipes by file name, for an error that names
// one that is missing; it reads none of them.
func (s *Store) projectNames() string {
	entries, _ := os.ReadDir(s.ProjectsDir())
	var names []string
	for _, e := range entries {
		if !e.IsDir() && !strings.HasPrefix(e.Name(), ".") && strings.HasSuffix(e.Name(), ".json") {
			names = append(names, strings.TrimSuffix(e.Name(), ".json"))
		}
	}
	if len(names) == 0 {
		return "none; see tspace project save --help"
	}
	return strings.Join(names, ", ")
}

func (s *Store) ListProjects() ([]*Project, error) {
	entries, err := os.ReadDir(s.ProjectsDir())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var projects []*Project
	var skipped SkippedError
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		p, err := s.LoadProject(strings.TrimSuffix(e.Name(), ".json"))
		if err != nil {
			// One malformed recipe must not hide the rest of the shelf.
			skipped.Errs = append(skipped.Errs, err)
			continue
		}
		projects = append(projects, p)
	}
	return projects, skipped.orNil() // ReadDir sorts by filename.
}

// CreatePrepared publishes a fully validated task without overwriting an
// existing record or leaving a partial task when recipe validation fails.
func (s *Store) CreatePrepared(t *Task) error {
	if err := ValidateName("task", t.Name); err != nil {
		return err
	}
	if err := s.upgrade(t); err != nil {
		return err
	}
	if err := publishJSON(s.path(t.Name), t, false); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("%q: %w", t.Name, ErrExists)
		}
		return err
	}
	return nil
}

func publishJSON(path string, value any, replace bool) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".publish-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if replace {
		err = os.Rename(f.Name(), path)
	} else {
		err = os.Link(f.Name(), path)
	}
	if err != nil {
		return fmt.Errorf("save %s: %w", path, err)
	}
	return nil
}
